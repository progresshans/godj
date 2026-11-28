//go:build darwin || linux

package godj

import (
	"encoding/json"
	productcheck "github.com/progresshans/godj/internal/projectcheck"
	"github.com/progresshans/godj/project/compatibility"
	"github.com/progresshans/godj/version"
)

// These in-process launchers use an explicit development identity. They do not
// provide evidence of independently installed module/binary compatibility.
func commandFixtureToolInfo() compatibility.Info {
	info := compatibility.Current()
	info.Framework = version.Info{Module: version.Module, Origin: version.Development}
	return info
}
func commandFixtureCompatibility() productcheck.ProcessResult {
	data, err := json.Marshal(commandFixtureToolInfo())
	if err != nil {
		return productcheck.ProcessResult{ExitCode: 1}
	}
	data = append(data, '\n')
	return productcheck.ProcessResult{Started: true, ExitCode: 0, DirectReaps: 1, Stdout: data, StdoutScalar: productcheck.StreamScalar{RetainedBytes: len(data)}}
}
func (*gdj0055BuildBackend) ToolInfo() compatibility.Info            { return commandFixtureToolInfo() }
func (*gdj0055ForbiddenBuildBackend) ToolInfo() compatibility.Info   { return commandFixtureToolInfo() }
func (*migrationCommandProcessBackend) ToolInfo() compatibility.Info { return commandFixtureToolInfo() }
func (*migrationProjectCheckBackend) ToolInfo() compatibility.Info   { return commandFixtureToolInfo() }
func (*migrationSQLRenderingArgvProbeBackend) ToolInfo() compatibility.Info {
	return commandFixtureToolInfo()
}
func (*migrationSQLRenderingForbiddenBackend) ToolInfo() compatibility.Info {
	return commandFixtureToolInfo()
}
func (*migrationSQLRenderingProcessBackend) ToolInfo() compatibility.Info {
	return commandFixtureToolInfo()
}
func (*migrationStatusProcessBackend) ToolInfo() compatibility.Info { return commandFixtureToolInfo() }
func (*migrationTargetPlanProcessOwner) ToolInfo() compatibility.Info {
	return commandFixtureToolInfo()
}
func (*migrationWriterBackend) ToolInfo() compatibility.Info { return commandFixtureToolInfo() }
