package terraform

import (
	"testing"

	"github.com/imohiyoko/oekaki/core"
)

// A large estate is mostly modules, so the module axis has to survive nesting
// rather than flattening to one level.
func TestModuleAxisNests(t *testing.T) {
	g := parseFile(t, "testdata/modules.json", Options{})

	outer, ok := g.Group("module:module.platform")
	if !ok {
		t.Fatal("module:module.platform is missing")
	}
	if outer.Parent != nil {
		t.Errorf("the outermost module has parent %v, want nil", *outer.Parent)
	}

	inner, ok := g.Group("module:module.platform.module.network")
	if !ok {
		t.Fatal("the nested module group is missing")
	}
	if inner.Parent == nil || *inner.Parent != outer.ID {
		t.Errorf("nested module parent = %v, want %q", inner.Parent, outer.ID)
	}

	n, ok := g.Node("module.platform.module.network.aws_instance.probe")
	if !ok {
		t.Fatal("the module-nested instance is missing")
	}
	want := "module:module.platform/module:module.platform.module.network"
	if got := n.GroupOn(core.AxisModule); got != want {
		t.Errorf("module path = %q, want %q", got, want)
	}
}

// References inside a module still have to resolve, so containment keeps
// working for resources that never appear in the root module.
func TestContainmentWorksInsideModules(t *testing.T) {
	g := parseFile(t, "testdata/modules.json", Options{})

	n, ok := g.Node("module.platform.module.network.aws_instance.probe")
	if !ok {
		t.Fatal("the module-nested instance is missing")
	}
	want := "module.platform.module.network.aws_subnet.a"
	if got := n.GroupOn(core.AxisNetwork); got != want {
		t.Errorf("network path = %q, want %q", got, want)
	}
}
