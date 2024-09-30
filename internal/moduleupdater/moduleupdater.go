// Package with functionality for updating a MODULE.bazel file.
package moduleupdater

import (
	"github.com/bazelbuild/buildtools/build"
	"github.com/golang/glog"
)

type MatchSpec struct {
	GoImportPath string
}

// Updates the MODULE.bazel file specified by f so that any go_dep.archive_override calls
// at top-level in the file point to the archive with the given url and sha256.
func (ms *MatchSpec) UpdateFile(f *build.File, sha256, url string) (bool, error) {
	for _, expr := range f.Stmt {
		// Seek out an go_deps.archive_override()
		call, ok := expr.(*build.CallExpr)
		if !ok {
			continue
		}
		dotExpr, ok := call.X.(*build.DotExpr)
		if !ok {
			continue
		}
		if dotExpr.Name != "archive_override" {
			continue
		}
		glog.Infof("processing archive_override call %v", call)
		rule := build.NewRule(call)
		if rule.AttrString("path") != ms.GoImportPath {
			continue
		}
		rule.SetAttr("sha256", &build.StringExpr{Value: sha256})
		rule.SetAttr("urls", &build.ListExpr{
			List: []build.Expr{
				&build.StringExpr{Value: url},
			},
		})
		return true, nil
	}
	return false, nil
}

func (ms *MatchSpec) Matches(f *build.File) bool {
	return matchingRule(f, *ms) != nil
}

func matchingRule(f *build.File, matchSpec MatchSpec) *build.Rule {
	for _, expr := range f.Stmt {
		// Seek out an go_deps.archive_override()
		call, ok := expr.(*build.CallExpr)
		if !ok {
			continue
		}
		dotExpr, ok := call.X.(*build.DotExpr)
		if !ok {
			continue
		}
		if dotExpr.Name != "archive_override" {
			continue
		}
		glog.Infof("processing archive_override call %v", call)
		rule := build.NewRule(call)
		if rule.AttrString("path") == matchSpec.GoImportPath {
			return rule
		}

	}
	return nil
}
