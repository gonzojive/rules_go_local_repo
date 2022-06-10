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

	"github.com/bazelbuild/buildtools/build"
	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"golang.org/x/sync/errgroup"
)

var (
	inputDir      = flag.String("input", "", "Input directory.")
	addr          = flag.String("http_addr", "localhost:8673", "Serving address to use for HTTP server.")
	workspacePath = flag.String("workspce", "/home/red/tmp/EXAMPLE.bazel", "Workspace file")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		glog.Exitf("error: %v", err)
	}
}

func run() error {

	eg, ctx := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		wsBytes, err := ioutil.ReadFile(*workspacePath)
		if err != nil {
			return err
		}
		parsedFile, err := build.ParseWorkspace(*workspacePath, wsBytes)
		if err != nil {
			return err
		}
		glog.Infof("successfully parsed workspace: %v", parsedFile)
		return nil
	})
	eg.Go(func() error {
		return watchDirAndGenerateZips(ctx, ctx.Done(), *inputDir, func(zipFile []byte) {
		})
	})
	eg.Go(func() error {
		http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			content, sha, err := doZip()
			if err != nil {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(fmt.Sprintf("error zipping dir: %v", err)))
				return
			}

			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="repo-%s.zip"`, sha))
			w.WriteHeader(http.StatusOK)
			w.Write(content)

		})
		log.Println("Listening... at http://" + *addr)
		return http.ListenAndServe(*addr, nil)

	})
	return eg.Wait()
}

func doZip() (content []byte, sha string, err error) {
	outZip := &bytes.Buffer{}
	if err := zipDir(*inputDir, outZip); err != nil {
		return nil, "", err
	}
	h := sha256.New()
	h.Write([]byte("hello world\n"))
	hFormatted := fmt.Sprintf("%x", h.Sum(nil))

	glog.Infof("got zip of length %d; sha256=%q", len(outZip.Bytes()), hFormatted)
	return outZip.Bytes(), hFormatted, nil
}

func watchDirAndGenerateZips(ctx context.Context, done <-chan struct{}, dir string, fn func(zipFile []byte)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("error watching dir %q: %w", dir, err)
	}
	defer watcher.Close()

	eg := &errgroup.Group{}
	eg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-done:
				return nil
			case event, ok := <-watcher.Events:
				if !ok {
					return nil
				}
				glog.Infof("event: %v", event)
				if event.Op&fsnotify.Write == fsnotify.Write {
					glog.Infof("modified file: %v", event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
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
		if info.IsDir() {
			return nil
		}
		watchedSet[path] = true
		return nil
	}); err != nil {
		return fmt.Errorf("error generating zip: %w", err)
	}

	for d := range watchedSet {
		if err = watcher.Add(dir); err != nil {
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
