# rules_go_local_repo

An esoteric tool for making modifications to dependent libraries for bazel.

* Addresses https://github.com/bazelbuild/rules_go/issues/283
* serves a zip file with the contents of a directory over HTTP
* Whenever the directory contents are updated, updates a bazel WORKSPACE
  repository rule



## Setup

Assume you have a **WORKSPACE.bazel:**

```starlark
go_repository(
    name = "com_example_some_repo",
    sha256 = "5982e5463f171da99e3bdaeff8c0f48283a7a5f396ec5282910b9e8a49c0dd7e",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/bazel-gazelle/releases/download/v0.25.0/bazel-gazelle-v0.25.0.tar.gz",
    ],
    type = "zip",
)
```

### Running from a local installation of the tool

```shell
go install github.com/gonzojive/rules_go_local_repo@latest
```

```shell
rules_go_local_repo --alsologtostderr --input "/home/person/git/my_copy_of_dep" --rule_name "com_example_some_repo" --workspace "/home/person/git/my_repo/WORKSPACE"
```

The WORKSPACE.bazel file will be continuously updated to something like the
following:

```
go_repository(
    name = "com_example_some_repo",
    sha256 = "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447",
    urls = ["http://localhost:8673/?sha256=a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447"],
    type = "zip",
)
```


### Running from bazel

```shell
bazel run @com_gonzojive_rules_go_local_repo//:rules_go_local_repo -- --alsologtostderr --input "/home/person/git/my_copy" --rule_name "com_example_some_repo" --workspace "/home/person/git/my_copy"
```

After installing the dependency in your WORKSPACE:

```starlark
# TODO
```