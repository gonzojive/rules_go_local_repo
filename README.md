# rules_go_local_repo

An esoteric tool for making modifications to dependent libraries for bazel.

* Addresses https://github.com/bazelbuild/rules_go/issues/283
* serves a zip file with the contents of a directory over HTTP
* Whenever the directory contents are updated, updates a bazel WORKSPACE
  repository rule



## Setup

#### Install
```shell
git clone https://github.com/gonzojive/rules_go_local_repo.git
cd rules_go_local_repo
```

#### Run

Assume you have some MODULE.bazel file at `/path/to/some/MODULE.bazel`
and you want to use a local version of the `github.com/bazel-contrib/rules_jvm` go module that's checked out to  `/my/code/rules_jvm`.

```shell
bazel run //:rules_go_local_repo -- \
  --module_file /path/to/some/MODULE.bazel \
  --input_dir /my/code/rules_jvm \
  --http_addr localhost:8674 \
  --import_path "github.com/bazel-contrib/rules_jvm"
  --alsologtostderr
```

After installing the dependency in your WORKSPACE:

Before the tool runs, your MODULE.bazel file might look like

```starlark
module(
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
    sha256 = "...",
    urls = ["..."],
)
```

While the tool is running, the archive_override will be continuously
updated every time something in the local directory changes:

```starlark
module(
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
    sha256 = "4fae82b45702920d6c3d9d18e452b2477c7c98e05b4ec027aaca2bfcf1f35ee8",
    urls = ["http://localhost:8674/by-sha256/4fae82b45702920d6c3d9d18e452b2477c7c98e05b4ec027aaca2bfcf1f35ee8.zip"],
)
```
