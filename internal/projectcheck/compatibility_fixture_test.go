//go:build darwin || linux

package projectcheck

import (
	"encoding/json"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/version"
)

// These adapters model a development launcher explicitly. Native process tests
// use a separately built godj executable to verify real compiler identities.
func fixtureToolInfo() compatibility.Info {
	info := compatibility.Current()
	info.Framework = version.Info{Module: version.Module, Origin: version.Development}
	return info
}

func fixtureCompatibilityProcess() ProcessResult {
	data, err := json.Marshal(fixtureToolInfo())
	if err != nil {
		panic(err)
	}
	return ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: data, StdoutScalar: StreamScalar{RetainedBytes: len(data)}}
}
func (*scriptedBackend) ToolInfo() compatibility.Info                    { return fixtureToolInfo() }
func (backendFunc) ToolInfo() compatibility.Info                         { return fixtureToolInfo() }
func (*migrateScriptedBackend) ToolInfo() compatibility.Info             { return fixtureToolInfo() }
func (*showMigrationsScriptedBackend) ToolInfo() compatibility.Info      { return fixtureToolInfo() }
func (*sqlMigrateScriptedBackend) ToolInfo() compatibility.Info          { return fixtureToolInfo() }
func (*runserverSequenceBackend) ToolInfo() compatibility.Info           { return fixtureToolInfo() }
func (*makemigrationsRunBackend) ToolInfo() compatibility.Info           { return fixtureToolInfo() }
func (*liveMakemigrationsBackend) ToolInfo() compatibility.Info          { return fixtureToolInfo() }
func (*createsuperuserBuildBackend) ToolInfo() compatibility.Info        { return fixtureToolInfo() }
func (*actualStageBackend) ToolInfo() compatibility.Info                 { return fixtureToolInfo() }
func (startProjectBackendFunc) ToolInfo() compatibility.Info             { return fixtureToolInfo() }
func (*phaseCTempMutatingInventoryBackend) ToolInfo() compatibility.Info { return fixtureToolInfo() }
