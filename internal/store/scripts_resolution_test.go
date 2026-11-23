package store

import (
	"errors"
	"reflect"
	"testing"

	"loom/internal/graph"
)

func scriptWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "script-wf", Name: "Scripts", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "js", Type: "code.run", TypeVersion: 1, Name: "JS", Config: map[string]any{"language": "javascript"}},
			{
				ID: "py", Type: "code.python", TypeVersion: 1, Name: "Python",
				Config: map[string]any{"requirements": []any{"idna==3.10"}},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "js", Port: "main"}},
			{From: graph.Endpoint{Node: "js", Port: "main"}, To: graph.Endpoint{Node: "py", Port: "main"}},
		},
	}
}

// Phase 4 D4-1/D4-3/D4-4: publish inlines the pinned script source, fills
// runtime defaults only where the node sets none, unions requirements and
// keeps node-local source as the winning escape hatch.
func TestResolveScriptRefs(t *testing.T) {
	st := testProfileStore(t)
	js, err := st.CreateScript(&Script{
		ID: "scr-js", Name: "Greet", Language: ScriptLanguageJavaScript,
		Source: "return { hello: true };", TimeoutSeconds: 120,
	}, "u")
	if err != nil {
		t.Fatalf("create js: %v", err)
	}
	py, err := st.CreateScript(&Script{
		ID: "scr-py", Name: "Shape", Language: ScriptLanguagePython,
		Source: "return {'ok': True}", Requirements: []string{"certifi==2025.1.1", "idna==3.10"},
		TimeoutSeconds: 300, MaxMemoryMB: 1024,
	}, "u")
	if err != nil {
		t.Fatalf("create py: %v", err)
	}

	wf := scriptWorkflow()
	wf.Nodes[1].Config["scriptRef"] = js.ID
	wf.Nodes[2].Config["scriptRef"] = py.ID
	refs, err := st.ResolveScriptRefs(wf)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if refs["scr-js"] != js.Version || refs["scr-py"] != py.Version {
		t.Fatalf("refs = %v", refs)
	}

	jsNode := wf.Nodes[1].Config
	if jsNode["source"] != "return { hello: true };" {
		t.Fatalf("js source = %v", jsNode["source"])
	}
	if jsNode["scriptRef"] != "scr-js" {
		t.Fatalf("js ref not retained: %v", jsNode["scriptRef"])
	}
	if jsNode["timeoutSeconds"] != 120 {
		t.Fatalf("js default timeout = %v (script default should fill the absent key)", jsNode["timeoutSeconds"])
	}

	pyNode := wf.Nodes[2].Config
	if pyNode["source"] != "return {'ok': True}" {
		t.Fatalf("py source = %v", pyNode["source"])
	}
	if pyNode["timeoutSeconds"] != 300 || pyNode["maxMemoryMB"] != 1024 {
		t.Fatalf("py defaults = %v/%v", pyNode["timeoutSeconds"], pyNode["maxMemoryMB"])
	}
	// Union: node had idna==3.10, script brings certifi + idna (deduped).
	if !reflect.DeepEqual(pyNode["requirements"], []any{"idna==3.10", "certifi==2025.1.1"}) {
		t.Fatalf("py requirements = %v", pyNode["requirements"])
	}

	// Node-set values are NOT overwritten by script defaults.
	// (timeoutSeconds on js above proves the fill case; here a node
	// timeout survives.)
	wf2 := scriptWorkflow()
	wf2.Nodes[1].Config["scriptRef"] = js.ID
	wf2.Nodes[1].Config["timeoutSeconds"] = 30.0
	if _, err := st.ResolveScriptRefs(wf2); err != nil {
		t.Fatalf("resolve 2: %v", err)
	}
	if wf2.Nodes[1].Config["timeoutSeconds"] != 30.0 {
		t.Fatalf("node timeout overwritten: %v", wf2.Nodes[1].Config["timeoutSeconds"])
	}

	// Node-local source wins: the ref stays but nothing is inlined.
	wf3 := scriptWorkflow()
	wf3.Nodes[1].Config["source"] = "return { authored: true };"
	wf3.Nodes[1].Config["scriptRef"] = js.ID
	refs3, err := st.ResolveScriptRefs(wf3)
	if err != nil {
		t.Fatalf("resolve 3: %v", err)
	}
	if len(refs3) != 0 {
		t.Fatalf("escape hatch still pinned a usage: %v", refs3)
	}
	if wf3.Nodes[1].Config["source"] != "return { authored: true };" {
		t.Fatalf("authored source replaced: %v", wf3.Nodes[1].Config["source"])
	}

	// A language mismatch is a named error.
	wf4 := scriptWorkflow()
	wf4.Nodes[1].Config["scriptRef"] = py.ID
	if _, err := st.ResolveScriptRefs(wf4); err == nil || err.Error() == "" {
		t.Fatalf("language mismatch accepted: %v", err)
	}

	// A missing script is a named error (wrapped with the node id).
	wf5 := scriptWorkflow()
	wf5.Nodes[1].Config["scriptRef"] = "ghost"
	if _, err := st.ResolveScriptRefs(wf5); !errors.Is(err, ErrScriptNotFound) {
		t.Fatalf("missing script = %v", err)
	}
}

// The profile bundle's script fills the node's empty scriptRef slot
// (D4-1); a node that references its own script keeps it.
func TestResolveProfileRefsInjectsBundleScript(t *testing.T) {
	st := testProfileStore(t)
	js, err := st.CreateScript(&Script{ID: "scr-a", Name: "A", Language: ScriptLanguageJavaScript, Source: "return 1;"}, "u")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	other, err := st.CreateScript(&Script{ID: "scr-b", Name: "B", Language: ScriptLanguageJavaScript, Source: "return 2;"}, "u")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "prof-js", Name: "JS defaults", NodeType: "code.run",
		Config: map[string]any{"timeoutSeconds": 60.0},
		Bundle: &ProfileBundle{Script: js.ID},
	}, "u"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	// Node without its own ref: the bundle script lands.
	wf := scriptWorkflow()
	wf.Nodes[1].ProfileRef = "prof-js"
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profile refs: %v", err)
	}
	if wf.Nodes[1].Config["scriptRef"] != js.ID {
		t.Fatalf("bundle script not injected: %#v", wf.Nodes[1].Config)
	}

	// Node with its own ref: the node wins.
	wf2 := scriptWorkflow()
	wf2.Nodes[1].ProfileRef = "prof-js"
	wf2.Nodes[1].Config["scriptRef"] = other.ID
	if _, err := st.ResolveProfileRefs(wf2); err != nil {
		t.Fatalf("resolve profile refs 2: %v", err)
	}
	if wf2.Nodes[1].Config["scriptRef"] != other.ID {
		t.Fatalf("node scriptRef overridden by bundle: %#v", wf2.Nodes[1].Config)
	}

	// The bundle ref is indexed for the delete guard.
	names, err := st.ProfilesReferencingAsset("script", js.ID)
	if err != nil || len(names) != 1 {
		t.Fatalf("profiles referencing script = %v, %v", names, err)
	}
}

// The upgrade path follows a bundle script change: the stale inlined
// source (identified via the usage-index pin) is cleared so the new
// script version re-resolves at the next publish, while an authored
// source survives untouched.
func TestUpgradeFollowsBundleScript(t *testing.T) {
	st := testProfileStore(t)
	jsV1, err := st.CreateScript(&Script{ID: "scr-up", Name: "Up", Language: ScriptLanguageJavaScript, Source: "return 1;"}, "u")
	if err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "prof-up", Name: "Up profile", NodeType: "code.run",
		Config: map[string]any{"timeoutSeconds": 60.0},
		Bundle: &ProfileBundle{Script: "scr-up"},
	}, "u"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	// A workflow that ran the pinned script v1: node carries the inlined
	// v1 source and the bundle-injected ref, and the usage index pinned v1.
	wf := scriptWorkflow()
	wf.ID = "up-wf"
	wf.Nodes[1].ProfileRef = "prof-up"
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profile refs: %v", err)
	}
	if _, err := st.ResolveScriptRefs(wf); err != nil {
		t.Fatalf("resolve script refs: %v", err)
	}
	if wf.Nodes[1].Config["source"] != "return 1;" {
		t.Fatalf("v1 source not inlined: %#v", wf.Nodes[1].Config)
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := st.RecordScriptUsage("scr-up", jsV1.Version, wf.ID, 1); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	// A real publish also pins the profile version.
	if err := st.RecordConfigProfileUsage("prof-up", 1, wf.ID, 1); err != nil {
		t.Fatalf("record profile usage: %v", err)
	}

	// The script moves to v2 under the same id (the bundle reference is
	// unchanged — the version is what follows).
	if _, err := st.UpdateScript(&Script{ID: "scr-up", Name: "Up", Language: ScriptLanguageJavaScript, Source: "return 2;"}, 1, "u"); err != nil {
		t.Fatalf("update script: %v", err)
	}

	// Upgrade: the profile version is unchanged so the upgrade skips the
	// node — the follow happens at the next resolve, where the stored
	// source is recognized as script-inlined (it matches one of the
	// script's versions, memory #1385) and replaced with v2.
	upgraded, plan, err := st.UpgradeWorkflowProfiles(wf)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if len(plan) != 0 {
		t.Fatalf("plan = %+v, want none (the profile and bundle are unchanged)", plan)
	}
	if upgraded.Nodes[1].Config["scriptRef"] != "scr-up" {
		t.Fatalf("ref lost in upgrade: %#v", upgraded.Nodes[1].Config)
	}
	if _, err := st.ResolveScriptRefs(upgraded); err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if upgraded.Nodes[1].Config["source"] != "return 2;" {
		t.Fatalf("new source not inlined: %#v", upgraded.Nodes[1].Config)
	}

	// An authored source (diverging from every script version) survives
	// resolution — the escape hatch.
	wfAuthored := scriptWorkflow()
	wfAuthored.ID = "authored-wf"
	wfAuthored.Nodes[1].Config["scriptRef"] = "scr-up"
	wfAuthored.Nodes[1].Config["source"] = "return { authored: true };"
	if _, err := st.ResolveScriptRefs(wfAuthored); err != nil {
		t.Fatalf("resolve authored: %v", err)
	}
	if wfAuthored.Nodes[1].Config["source"] != "return { authored: true };" {
		t.Fatalf("authored source overwritten: %#v", wfAuthored.Nodes[1].Config)
	}
}
