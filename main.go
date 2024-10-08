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
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/bazelbuild/buildtools/build"
	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"github.com/gonzojive/rules_go_local_repo/internal/moduleupdater"
	"github.com/gonzojive/rules_go_local_repo/util/debouncer"
	"github.com/julienschmidt/httprouter"
	gitignore "github.com/sabhiram/go-gitignore"
	"golang.org/x/sync/errgroup"
)

var (
	inputDir   = flag.String("input_dir", "", "Input directory.")
	addr       = flag.String("http_addr", "localhost:8673", "Serving address to use for HTTP server.")
	modulePath = flag.String("module_file", "", "Path of a MODULE.bazel file.")
	importPath = flag.String("import_path", "", "Go import path of the go_deps.archive_override to be updated.")
)

// debounceDelay is the delay after an update to the directory until a new zip is generated.
const debounceDelay = time.Millisecond * 500

func formatURL(sha256 string) string {
	return fmt.Sprintf("http://%s/by-sha256/%s.zip", *addr, sha256)
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
	if *modulePath == "" {
		return fmt.Errorf("must pass non-empty --module_file flag")
	}
	if *addr == "" {
		return fmt.Errorf("must pass non-empty --http_addr flag")
	}
	if *importPath == "" {
		return fmt.Errorf("must pass non-empty --import_path flag")
	}

	if err := ensureModuleMatches(); err != nil {
		return err
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
			glog.Infof("new zip file prepared with sha256 = %q", zipContents.sha256)

			moduleBytes, err := os.ReadFile(*modulePath)
			if err != nil {
				return fmt.Errorf("failed to read MODULE.bazel file at %q: %w", *modulePath, err)
			}
			parsedFile, err := build.ParseModule(*modulePath, moduleBytes)
			if err != nil {
				return err
			}

			if err := updateRelevantHTTPArchiveRules(parsedFile, z); err != nil {
				return fmt.Errorf("error updating workspace file: %w", err)
			}
			if err := os.WriteFile(*modulePath, build.Format(parsedFile), 0664); err != nil {
				return fmt.Errorf("error writing updated workspace file %q: %w", *modulePath, err)
			}
			glog.Infof("successfully wrote new MODULE.bazel with new hash %s", z.sha256)
			return nil
		}, func(ioErr error) error { return ioErr })
	})
	eg.Go(func() error {
		router := httprouter.New()
		router.GET("/", func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf("please zip archives using /by-sha256/\n\n")))
			if zipContents == nil {
				w.Write([]byte(fmt.Sprintf("zip archive of %q not currently available", *inputDir)))
			} else {
				url := fmt.Sprintf("http://%s/by-sha256/%s", *addr, zipContents.sha256)
				w.Write([]byte(fmt.Sprintf(`zip archive of %q is available with sha256 %s:

%s`, *inputDir, zipContents.sha256, url)))
			}
		})
		router.GET("/by-sha256/:expectedhash", func(w http.ResponseWriter, r *http.Request, params httprouter.Params) {
			wantSHA256 := strings.TrimSuffix(params.ByName("expectedhash"), ".zip")

			if zipContents == nil {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("zip file for directory is not available"))
				return
			}
			if wantSHA256 != "" && wantSHA256 != zipContents.sha256 {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(fmt.Sprintf("zip hash is %q but wanted %q", zipContents.sha256, wantSHA256)))
				return
			}

			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="repo-%s.zip"`, zipContents.sha256))
			w.WriteHeader(http.StatusOK)
			w.Write(zipContents.contents)

		})
		log.Println("Listening... at http://" + *addr)
		return http.ListenAndServe(*addr, router)

	})
	return eg.Wait()
}

func ensureModuleMatches() error {
	moduleBytes, err := os.ReadFile(*modulePath)
	if err != nil {
		return fmt.Errorf("failed to read MODULE.bazel file at %q: %w", *modulePath, err)
	}
	parsedFile, err := build.ParseModule(*modulePath, moduleBytes)
	if err != nil {
		return err
	}
	if (&moduleupdater.MatchSpec{
		GoImportPath: *importPath,
	}).Matches(parsedFile) {
		return nil
	}
	return fmt.Errorf(`%s doesn't contain a section like

go_deps.archive_override(
    path = %q,
    sha256 = "...",
    urls = ["..."],
)
	
Add such a section to your MODULE.bazel file.`, *modulePath, *importPath)
}

func updateRelevantHTTPArchiveRules(f *build.File, z *zippedDir) error {
	updated, err := (&moduleupdater.MatchSpec{
		GoImportPath: *importPath,
	}).UpdateFile(f, z.sha256, formatURL(z.sha256))

	if err != nil {
		return err
	}

	if !updated {
		glog.Warningf("failed to update MODULE.bazel file, could not find existing go_dep with archive_override")
		return nil
	}
	return nil
}

func attrDefn(call *build.CallExpr, key string) *build.AssignExpr {
	for _, kv := range call.List {
		as, ok := kv.(*build.AssignExpr)
		if !ok {
			continue
		}
		k, ok := as.LHS.(*build.Ident)
		if !ok || k.Name != key {
			continue
		}
		return as
	}
	return nil
}

func doZip() (*zippedDir, error) {
	outZip := &bytes.Buffer{}
	if err := zipDir(*inputDir, outZip); err != nil {
		return nil, err
	}
	zipBytes := outZip.Bytes()
	h := sha256.New()
	h.Write(zipBytes)
	hFormatted := fmt.Sprintf("%x", h.Sum(nil))

	return &zippedDir{hFormatted, zipBytes}, nil
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

var builtinGitIgnoreLines = []string{
	".git",
}

func zipDir(dir string, writer io.Writer) error {
	w := zip.NewWriter(writer)
	defer w.Close()

	ignoreFileBytes, err := os.ReadFile(path.Join(dir, ".gitignore"))
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("unexpected error opening .gitignore file: %w", err)
		}
	}
	lines := strings.Split(string(ignoreFileBytes), "\n")
	lines = append(lines, builtinGitIgnoreLines...)
	ignore := gitignore.CompileIgnoreLines(lines...)

	if err := walkDir(dir, func(path string, info os.FileInfo) error {

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("error coming up with relative path for file %q", relPath)
		}

		if ignore.MatchesPath(relPath) {
			glog.Infof("ignoring .gitignored path %s", relPath)
			return nil
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
