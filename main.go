// Program local_repo_http_server is used to quickly serve a .zip file with the contents of a directory.
//
// This works around an issue with go_repository not supporting local paths. The solution as implemented here is to bring up a local http server. In addition, the hash of the zip file is updated in the WORKSPACE file rule so that bazel knows to refresh the repository.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bazelbuild/buildtools/build"
	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"github.com/gonzojive/rules_go_local_repo/util/debouncer"
	"golang.org/x/sync/errgroup"
)

var (
	inputDir      = flag.String("input", "", "Input directory.")
	addr          = flag.String("http_addr", "localhost:8673", "Serving address to use for HTTP server.")
	workspacePath = flag.String("workspace", "", "Workspace file")
	ruleName      = flag.String("rule_name", "", "Name of the http_archive rule in the workspace file that should be updated to point at the running server.")
)

// debounceDelay is the delay after an update to the directory until a new zip is generated.
const debounceDelay = time.Millisecond * 500

func formatURL(sha256 string) string {
	return fmt.Sprintf("http://%s/?sha256=%s", *addr, sha256)
}

type zippedDir struct {
	sha256   string
	contents []byte
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		glog.Exitf("error: %v", err)
	}
}

func run() error {
	if *ruleName == "" {
		return fmt.Errorf("must pass non-empty --rule_name flag")
	}
	if *workspacePath == "" {
		return fmt.Errorf("must pass non-empty --workspace flag")
	}
	if *addr == "" {
		return fmt.Errorf("must pass non-empty --http_addr flag")
	}

	eg, ctx := errgroup.WithContext(context.Background())

	var zipContents *zippedDir

	eg.Go(func() error {
		return watchDirAndGenerateZips(ctx, ctx.Done(), *inputDir, func(z *zippedDir, err error) error {
			if err != nil {
				glog.Errorf("error generating zip file: %v", err)
				return nil
			}
			zipContents = z

			wsBytes, err := ioutil.ReadFile(*workspacePath)
			if err != nil {
				return fmt.Errorf("failed to read workspace file at %q: %w", *workspacePath, err)
			}
			parsedFile, err := build.ParseWorkspace(*workspacePath, wsBytes)
			if err != nil {
				return err
			}

			if err := updateRelevantHTTPArchiveRules(parsedFile, z); err != nil {
				return fmt.Errorf("error updating workspace file: %w", err)
			}
			if err := ioutil.WriteFile(*workspacePath, build.Format(parsedFile), 0664); err != nil {
				return fmt.Errorf("error writing updated workspace file %q: %w", *workspacePath, err)
			}
			glog.Infof("successfully wrote new workspace with new hash %s", z.sha256)
			return nil
		}, func(ioErr error) error { return ioErr })
	})
	eg.Go(func() error {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			wantSHA256 := r.URL.Query().Get("sha256")

			if zipContents == nil {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("zip file for directory is not available"))
				return
			}
			if wantSHA256 != "" && wantSHA256 != zipContents.sha256 {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusGone)
				w.Write([]byte(fmt.Sprintf("zip hash is %q but wanted %q", zipContents.sha256, wantSHA256)))
				return
			}

			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="repo-%s.zip"`, zipContents.sha256))
			w.WriteHeader(http.StatusOK)
			w.Write(zipContents.contents)

		})
		log.Println("Listening... at http://" + *addr)
		return http.ListenAndServe(*addr, nil)

	})
	return eg.Wait()
}

func updateRelevantHTTPArchiveRules(f *build.File, z *zippedDir) error {
	for _, rule := range f.Rules("http_archive") {
		if rule.Name() != *ruleName {
			continue
		}
		rule.SetAttr("sha256", &build.StringExpr{Value: z.sha256})
		rule.SetAttr("urls", &build.ListExpr{
			List: []build.Expr{
				&build.StringExpr{Value: formatURL(z.sha256)},
			},
		})
		glog.Infof("updated http_archive(name=%q, ...) at %s:%d", *ruleName, *workspacePath, rule.Call.Pos.Line)
	}
	return nil
}

func doZip() (*zippedDir, error) {
	outZip := &bytes.Buffer{}
	if err := zipDir(*inputDir, outZip); err != nil {
		return nil, err
	}
	h := sha256.New()
	h.Write([]byte("hello world\n"))
	hFormatted := fmt.Sprintf("%x", h.Sum(nil))

	return &zippedDir{hFormatted, outZip.Bytes()}, nil
}

func watchDirAndGenerateZips(ctx context.Context, done <-chan struct{}, dir string, fn func(zipFile *zippedDir, err error) error, transformIOErr func(error) error) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("error watching dir %q: %w", dir, err)
	}
	defer watcher.Close()

	eg, ctx := errgroup.WithContext(ctx)

	// Wait a bit after the last update within the directory before performing a
	// a new zip operation.
	debounce := debouncer.NewDebouncer(debounceDelay)
	eg.Go(func() error {
		return debounce.Listen(ctx, func() error {
			return fn(doZip())
		})
	})
	debounce.Trigger()

	eg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
				return nil
			case <-watcher.Events:
				debounce.Trigger()
			case err := <-watcher.Errors:
				glog.Errorf("file watcher error: %v", err)
				err = transformIOErr(err)
				if err != nil {
					return err
				}
			}
		}
	})

	watchedSet := map[string]bool{
		dir: true,
	}

	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error walking %q: %w", path, err)
		}
		if (info.Mode() & os.ModeSymlink) != 0 {
			return nil
		}
		watchedSet[path] = true
		return nil
	}); err != nil {
		return fmt.Errorf("error generating zip: %w", err)
	}

	for d := range watchedSet {
		if err = watcher.Add(d); err != nil {
			return fmt.Errorf("error adding %q to watch set", d)
		}
	}
	return eg.Wait()
}

func zipDir(dir string, writer io.Writer) error {
	w := zip.NewWriter(writer)
	defer w.Close()

	if err := walkDir(dir, func(path string, info os.FileInfo) error {
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("error coming up with relative path for file %q", relPath)
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		f, err := w.Create(relPath)
		if err != nil {
			return err
		}

		_, err = io.Copy(f, file)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("error generating zip: %w", err)
	}
	return nil
}

func walkDir(dir string, fn func(path string, info os.FileInfo) error) error {
	walker := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error walking %q: %w", path, err)
		}
		if info.IsDir() || (info.Mode()&os.ModeSymlink) != 0 {
			return nil
		}
		return fn(path, info)
	}
	if err := filepath.Walk(dir, walker); err != nil {
		return fmt.Errorf("error generating zip: %w", err)
	}
	return nil
}
