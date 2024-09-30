package moduleupdater

import (
	"testing"

	"github.com/bazelbuild/buildtools/build"
	"github.com/google/go-cmp/cmp"
)

func TestUpdateModuleFile(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		matchSpec  MatchSpec
		sha256     string
		url        string
		wantOutput string
		wantUpdate bool
		wantErr    bool
	}{
		{
			name: "Update existing archive_override",
			input: `module(
    name = "gazelle_kotlin",
    version = "0.0.1",
)

bazel_dep(name = "gazelle", version = "0.39.0")
bazel_dep(name = "rules_go", version = "0.50.1", repo_name = "io_bazel_rules_go")

go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")
use_repo(go_deps, "com_github_bazel_contrib_rules_jvm")
go_deps.archive_override(
    path = "github.com/bazel-contrib/rules_jvm",
    sha256 = "ab5be376d91240ef93cb4f3a6635997d5982b63ee72a49a5c3b8662bdeeba601",
    urls = [
        "http://localhost:8674/by-sha256/.zip",
    ],
)

non_module_dependencies = use_extension("//:extensions.bzl", "non_module_dependencies")
use_repo(non_module_dependencies, "tree-sitter-kotlin")
`,
			matchSpec: MatchSpec{
				GoImportPath: "github.com/bazel-contrib/rules_jvm",
			},
			sha256: "new_sha256",
			url:    "https://new.url/archive.zip",
			wantOutput: `module(
    name = "gazelle_kotlin",
    version = "0.0.1",
)

bazel_dep(name = "gazelle", version = "0.39.0")
bazel_dep(name = "rules_go", version = "0.50.1", repo_name = "io_bazel_rules_go")

go_deps = use_extension("@gazelle//:extensions.bzl", "go_deps")
go_deps.from_file(go_mod = "//:go.mod")
use_repo(go_deps, "com_github_bazel_contrib_rules_jvm")
go_deps.archive_override(
    path = "github.com/bazel-contrib/rules_jvm",
    sha256 = "new_sha256",
    urls = ["https://new.url/archive.zip"],
)

non_module_dependencies = use_extension("//:extensions.bzl", "non_module_dependencies")
use_repo(non_module_dependencies, "tree-sitter-kotlin")
`,
			wantUpdate: true,
			wantErr:    false,
		},
		{
			name: "Update and ignore extra attribute",
			input: `module(name = "xyz")

go_deps.archive_override(
    path = "github.com/bazel-contrib/rules_jvm",
    sha256 = "ab5be376d91240ef93cb4f3a6635997d5982b63ee72a49a5c3b8662bdeeba601",
    urls = ["http://localhost:8674/by-sha256/.zip"],
	something_else = "blah",
)`,
			matchSpec: MatchSpec{
				GoImportPath: "github.com/bazel-contrib/rules_jvm",
			},
			sha256: "new_sha256",
			url:    "https://xyz",
			wantOutput: `module(name = "xyz")

go_deps.archive_override(
    path = "github.com/bazel-contrib/rules_jvm",
    sha256 = "new_sha256",
    something_else = "blah",
    urls = ["https://xyz"],
)
`,
			wantUpdate: true,
			wantErr:    false,
		},
		{
			name: "No update upon a miss",
			input: `module(name = "xyz")

go_deps.archive_override(
    path = "github.com/bazel-contrib/rules_jvm",
    sha256 = "ab5be376d91240ef93cb4f3a6635997d5982b63ee72a49a5c3b8662bdeeba601",
    urls = ["http://localhost:8674/by-sha256/.zip"],
	something_else = "blah",
)`,
			matchSpec: MatchSpec{
				GoImportPath: "github.com/bazel-contrib/rules_jvmx",
			},
			sha256:     "new_sha256",
			url:        "https://xyz",
			wantUpdate: false,
			wantErr:    false,
		},
		// Add more test cases here as needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := build.ParseModule("MODULE.bazel", []byte(tt.input))
			if err != nil {
				t.Fatalf("Failed to parse input: %v", err)
			}

			updated, err := UpdateModuleFile(f, tt.matchSpec, tt.sha256, tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateModuleFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got, want := updated, tt.wantUpdate; got != want {
				t.Errorf("UpdateModuleFile() did not update the file")
			}

			if updated {
				want := tt.wantOutput
				got := string(build.Format(f))
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("UpdateModuleFile() output mismatch (-want +got):\n%s\n\ngot:\n%s\n\nwant:\n\n%s", diff, got, want)
				}
			}
		})
	}
}
