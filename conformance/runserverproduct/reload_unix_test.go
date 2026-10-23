//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/testenv"
)

type reloadProcess struct {
	command *exec.Cmd
	stdout  *testprocess.ReadinessBuffer
	stderr  *testprocess.Buffer
	done    chan struct{}
	err     error
	address string
	groups  []int
	stopped bool
}

func startReloadProcess(t *testing.T, global, project string, environment []string, prefix string) *reloadProcess {
	t.Helper()
	p := &reloadProcess{stdout: testprocess.NewReadinessBuffer(maximumRunserverOutput, prefix), stderr: testprocess.NewBuffer(maximumRunserverOutput), done: make(chan struct{})}
	p.command = exec.Command(global, "runserver", "--project", filepath.Join(project, "godj.toml"), "--addr", "127.0.0.1:0")
	p.command.Dir, p.command.Env = project, append([]string(nil), environment...)
	p.command.Stdout, p.command.Stderr = p.stdout, p.stderr
	p.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.command.WaitDelay = 2 * time.Second
	if err := p.command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.command.Wait(); close(p.done) }()
	t.Cleanup(func() {
		if !p.stopped {
			groups, _ := testprocess.OwnedGroups(p.command.Process.Pid)
			_ = testprocess.KillGroups(testprocess.MergeGroups(p.groups, groups), p.command.Process.Pid)
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
			}
		}
	})
	select {
	case p.address = <-p.stdout.Ready():
		if err := validateArticleReadinessAddress(p.address); err != nil {
			t.Fatal(err)
		}
		p.track(t)
	case <-p.done:
		t.Fatalf("reload startup exited: %v stdout_bytes=%d stderr=%s", p.err, len(p.stdout.String()), p.stderr.String())
	case <-time.After(runserverPreparationTimeout):
		t.Fatalf("reload startup timed out: stderr=%s", p.stderr.String())
	}
	return p
}

func (p *reloadProcess) track(t *testing.T) {
	t.Helper()
	groups, err := testprocess.OwnedGroups(p.command.Process.Pid)
	if err != nil || len(groups) < 2 {
		t.Fatal("reload process ownership", groups, err)
	}
	p.groups = testprocess.MergeGroups(p.groups, groups)
}

func (p *reloadProcess) await(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(runserverPreparationTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-p.done:
			t.Fatalf("reload exited during %s: %v stderr=%s", description, p.err, p.stderr.String())
		case <-deadline.C:
			t.Fatalf("reload timed out during %s: stdout=%s stderr=%s", description, p.stdout.String(), p.stderr.String())
		case <-poll.C:
		}
	}
}

func (p *reloadProcess) rejected(t *testing.T, code string, before int) {
	t.Helper()
	p.await(t, "candidate rejection", func() bool {
		return strings.Count(p.stderr.String(), "project_runserver_reload_rejected/"+code+"\n") > before
	})
}

func (p *reloadProcess) stop(t *testing.T, signal syscall.Signal) {
	t.Helper()
	p.track(t)
	if err := p.command.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(45 * time.Second):
		t.Fatal("reload shutdown did not join all candidates")
	}
	p.stopped = true
	if p.err != nil {
		t.Fatal("reload shutdown failed", p.err, p.stderr.String())
	}
	if err := testprocess.WaitAbsent(p.groups, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if p.stdout.Truncated() || p.stderr.Truncated() {
		t.Fatal("reload output was truncated")
	}
}

func reloadCLI(t *testing.T, global, project string, environment []string, args ...string) testprocess.CommandResult {
	t.Helper()
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: 5 * time.Minute, Output: maximumRunserverOutput}, project, environment, global, args...)
	if result.ExitCode != 0 || result.Stderr != "" {
		t.Fatalf("reload fixture %s: exit=%d stderr=%s", args[0], result.ExitCode, result.Stderr)
	}
	return result
}

func replaceReloadSource(t *testing.T, project, name, before, after string) {
	t.Helper()
	path := filepath.Join(project, name)
	data, err := os.ReadFile(path)
	if err != nil || bytes.Count(data, []byte(before)) != 1 {
		t.Fatal("reload source replacement was not exact", name, err)
	}
	writeReloadFile(t, project, name, strings.Replace(string(data), before, after, 1))
}

func writeReloadFile(t *testing.T, project, name, value string) {
	t.Helper()
	target := filepath.Join(project, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	// Editor-style atomic replacement must be observed as current bytes, even
	// when the old source file's inode no longer exists.
	pending := target + ".editing"
	if err := os.WriteFile(pending, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pending, target); err != nil {
		t.Fatal(err)
	}
}

type reloadNote struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	Summary *string `json:"summary"`
}
type reloadNotesState struct {
	Version string       `json:"version"`
	PID     int          `json:"pid"`
	Notes   []reloadNote `json:"notes"`
}

func (p *reloadProcess) notes(t *testing.T, version string) reloadNotesState {
	t.Helper()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	var state reloadNotesState
	p.await(t, "HTTP generation "+version, func() bool {
		response, err := client.Get("http://" + p.address + "/")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		var current reloadNotesState
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&current) != nil || current.Version != version || current.PID <= 1 {
			return false
		}
		state = current
		return true
	})
	p.groups = testprocess.MergeGroups(p.groups, []int{state.PID})
	return state
}

func (p *reloadProcess) save(title, summary, mode string) (reloadNotesState, int, error) {
	client := &http.Client{Timeout: runserverPreparationTimeout, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	response, err := client.Post("http://"+p.address+"/save?"+url.Values{"title": {title}, "summary": {summary}, "mode": {mode}}.Encode(), "text/plain", nil)
	if err != nil {
		return reloadNotesState{}, 0, err
	}
	defer response.Body.Close()
	var state reloadNotesState
	err = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&state)
	return state, response.StatusCode, err
}

func TestGlobalReloadPreservesActiveNotesAndDrainsCommittedWrites(t *testing.T) {
	repository := runserverRepositoryRoot(t)
	global := buildGlobalGodj(t, repository)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, control := t.TempDir(), t.TempDir()
	environment := testenv.With(testenv.Without(runserverEnvironment(t, "unused.sqlite3", workspace),
		articleSQLiteDatabaseEnv, "GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA"), map[string]string{
		"GODJ_SQLITE_DATABASE": filepath.Join(parent, "notes.sqlite3"), "GODJ_RELOAD_CONTROL": control,
	})
	reloadCLI(t, global, parent, environment, "startproject", "example.com/reload-notes", "notes", "--framework-source", repository)
	project := filepath.Join(parent, "notes")
	reloadCLI(t, global, project, environment, "startapp", "notes", "apps/notes")
	writeStartedAppSource(t, project, "modeldef/apps.go", `package modeldef
import (
 "github.com/progresshans/godj/schema"
 "example.com/reload-notes/apps/notes/modeldef"
)
func Applications() []schema.Definition { return []schema.Definition{{AppLabel: "site"}, modeldef.Definition()} }
`)
	writeStartedAppSource(t, project, "apps/notes/modeldef/schema.go", strings.ReplaceAll(startedAppDefinition(false), "line_items", "notes"))
	for _, args := range [][]string{{"generate"}, {"makemigrations"}, {"migrate"}} {
		reloadCLI(t, global, project, environment, args...)
	}
	writeStartedAppSource(t, project, "cmd/site/main.go", reloadNotesProgram(false))
	writeStartedAppSource(t, project, "internal/revision/revision.go", reloadRevisionSource)
	writeReloadFile(t, project, "internal/revision/version.txt", "v1")
	p := startReloadProcess(t, global, project, environment, "notes site listening on http://")
	first := p.notes(t, "v1")
	runPhase := func(name string, action func(*testing.T)) {
		t.Helper()
		if !t.Run(name, action) {
			t.FailNow()
		}
	}
	if len(first.Notes) != 0 {
		t.Fatal("fresh notes app has rows")
	}
	if state, status, err := p.save("before reload", "", ""); err != nil || status != 201 || state.Version != "v1" || len(state.Notes) != 1 {
		t.Fatal("initial write", state, status, err)
	}
	if current := p.notes(t, "v1"); current.PID != first.PID {
		t.Fatal("ordinary DB writes caused a rebuild")
	}

	runPhase("failed-build-keeps-current-runtime", func(t *testing.T) {
		before := strings.Count(p.stderr.String(), "project_runserver_reload_rejected/project_runtime_build_failed\n")
		writeReloadFile(t, project, "internal/revision/revision.go", "package revision\nfunc Current() string { return missingReloadVersion }\n")
		p.rejected(t, "project_runtime_build_failed", before)
		if current := p.notes(t, "v1"); current.PID != first.PID || len(current.Notes) != 1 {
			t.Fatal("failed build replaced live app", current)
		}
	})
	runPhase("failed-startup-and-descendant-are-reaped", func(t *testing.T) {
		before := strings.Count(p.stderr.String(), "project_runserver_reload_rejected/project_runtime_readiness_failed\n")
		writeReloadFile(t, project, "internal/revision/version.txt", "spawn-leak")
		writeStartedAppSource(t, project, "internal/revision/revision.go", reloadRevisionSource)
		p.rejected(t, "project_runtime_readiness_failed", before)
		data, err := os.ReadFile(filepath.Join(control, "leaked-pid"))
		if err != nil {
			t.Fatal("failed candidate did not exercise its real descendant", err)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil || pid <= 1 || syscall.Kill(pid, 0) == nil {
			t.Fatal("failed candidate left a descendant", pid, err)
		}
		if current := p.notes(t, "v1"); current.PID != first.PID || len(current.Notes) != 1 {
			t.Fatal("failed startup replaced live app", current)
		}
	})
	var second reloadNotesState
	runPhase("newer-source-cancels-late-startup", func(t *testing.T) {
		writeReloadFile(t, project, "internal/revision/version.txt", "wait-late")
		pid := awaitReloadMarkerPID(t, p, control, "waiting-wait-late")
		p.track(t)
		writeReloadFile(t, project, "internal/revision/version.txt", "v2")
		second = p.notes(t, "v2")
		if second.PID == pid || second.PID == first.PID || len(second.Notes) != 1 {
			t.Fatal("late candidate was published", second)
		}
		p.await(t, "discarded startup cleanup", func() bool {
			_, err := os.Stat(filepath.Join(control, "stopped-"+strconv.Itoa(pid)))
			return err == nil && syscall.Kill(pid, 0) != nil
		})
	})
	runPhase("old-request-commits-during-new-generation", func(t *testing.T) {
		type saved struct {
			state  reloadNotesState
			status int
			err    error
		}
		savedResult := make(chan saved, 1)
		go func() {
			state, status, err := p.save("during drain", "", "hold")
			savedResult <- saved{state, status, err}
		}()
		awaitReloadMarkerPID(t, p, control, "request-held")
		writeReloadFile(t, project, "internal/revision/version.txt", "v3")
		third := p.notes(t, "v3")
		if third.PID == second.PID || syscall.Kill(second.PID, 0) != nil {
			t.Fatal("old app disappeared before admitted request completed")
		}
		if _, err := os.Stat(filepath.Join(control, "stopped-"+strconv.Itoa(second.PID))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("old DB closed before write drain", err)
		}
		if err := os.WriteFile(filepath.Join(control, "release-request"), []byte("release"), 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-savedResult:
			if result.err != nil || result.status != 201 || result.state.Version != "v2" || len(result.state.Notes) != 2 {
				t.Fatal("draining write", result)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("draining write did not complete")
		}
		if state := p.notes(t, "v3"); len(state.Notes) != 2 {
			t.Fatal("committed draining write was not visible in the new runtime")
		}
	})
	runPhase("explicit-model-change-preserves-data-and-old-runtime", func(t *testing.T) {
		manifest, err := os.ReadFile(filepath.Join(project, ".godj/generated-manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		before := strings.Count(p.stderr.String(), "project_runserver_reload_rejected/generated_bundle_stale\n")
		writeStartedAppSource(t, project, "apps/notes/modeldef/schema.go", strings.ReplaceAll(startedAppDefinition(true), "line_items", "notes"))
		p.rejected(t, "generated_bundle_stale", before)
		currentManifest, err := os.ReadFile(filepath.Join(project, ".godj/generated-manifest.json"))
		if err != nil || !bytes.Equal(currentManifest, manifest) {
			t.Fatal("watch performed implicit generation", err)
		}
		if current := p.notes(t, "v3"); len(current.Notes) != 2 {
			t.Fatal("stale model changed durable data")
		}
		reloadCLI(t, global, project, environment, "generate")
		reloadCLI(t, global, project, environment, "makemigrations")
		before = strings.Count(p.stderr.String(), "project_runserver_reload_rejected/project_runtime_readiness_failed\n")
		writeStartedAppSource(t, project, "cmd/site/main.go", reloadNotesProgram(true))
		writeReloadFile(t, project, "internal/revision/version.txt", "unmigrated")
		p.rejected(t, "project_runtime_readiness_failed", before)
		if current := p.notes(t, "v3"); len(current.Notes) != 2 {
			t.Fatal("unmigrated runtime replaced old app")
		}
		reloadCLI(t, global, project, environment, "migrate")
		writeReloadFile(t, project, "internal/revision/version.txt", "v4")
		current := p.notes(t, "v4")
		if len(current.Notes) != 2 || current.Notes[0].Summary != nil || current.Notes[1].Summary != nil {
			t.Fatal("field addition changed existing rows", current)
		}
		state, status, err := p.save("after schema change", "new summary", "")
		if err != nil || status != 201 || len(state.Notes) != 3 || state.Notes[2].Summary == nil || *state.Notes[2].Summary != "new summary" {
			t.Fatal("new field write", state, status, err)
		}
	})
	runPhase("parent-termination-joins-active-and-preparing-apps", func(t *testing.T) {
		current := p.notes(t, "v4")
		writeReloadFile(t, project, "internal/revision/version.txt", "wait-shutdown")
		pending := awaitReloadMarkerPID(t, p, control, "waiting-wait-shutdown")
		p.track(t)
		p.stop(t, syscall.SIGTERM)
		for _, pid := range []int{current.PID, pending} {
			if _, err := os.Stat(filepath.Join(control, "stopped-"+strconv.Itoa(pid))); err != nil || syscall.Kill(pid, 0) == nil {
				t.Fatal("parent shutdown omitted app resource cleanup", pid, err)
			}
		}
	})
	assertRunserverWorkspaceEmpty(t, workspace)
	if strings.Contains(p.stdout.String()+p.stderr.String(), "private-reload-startup-marker") {
		t.Fatal("private startup cause was published")
	}
	writeReloadFile(t, project, "internal/revision/version.txt", "v4")
	restarted := startReloadProcess(t, global, project, environment, "notes site listening on http://")
	if current := restarted.notes(t, "v4"); len(current.Notes) != 3 || current.Notes[0].Title != "before reload" || current.Notes[1].Title != "during drain" || current.Notes[2].Title != "after schema change" {
		t.Fatal("fresh process lost durable writes", current)
	}
	restarted.stop(t, syscall.SIGINT)
	assertRunserverWorkspaceEmpty(t, workspace)
}

func awaitReloadMarkerPID(t *testing.T, process *reloadProcess, directory, name string) int {
	t.Helper()
	var pid int
	process.await(t, name, func() bool {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(string(data))
		return err == nil && pid > 1
	})
	return pid
}

const reloadRevisionSource = `package revision
import (
 _ "embed"
 "strings"
)
//go:embed version.txt
var version string
func Current() string { return strings.TrimSpace(version) }
`

func reloadNotesProgram(changed bool) string {
	readSummary, writeSummary := "", ""
	if changed {
		readSummary = "item.Summary = row.Summary"
		writeSummary = `if summary := request.HTTP().URL.Query().Get("summary"); summary != "" { create = create.WithSummary(summary) }`
	}
	return strings.NewReplacer("@@READ@@", readSummary, "@@WRITE@@", writeSummary).Replace(reloadNotesSource)
}

const reloadNotesSource = `package main
import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "os"
 "os/exec"
 "os/signal"
	"sort"
 "path/filepath"
 "strconv"
 "strings"
 "syscall"
 "time"
 "example.com/reload-notes/apps/notes/models"
 "example.com/reload-notes/internal/config"
 "example.com/reload-notes/internal/revision"
 "example.com/reload-notes/project"
 "github.com/progresshans/godj/project/compatibility"
 projectserver "github.com/progresshans/godj/project/server"
 "github.com/progresshans/godj/settings"
 "github.com/progresshans/godj/settings/database"
 "github.com/progresshans/godj/web"
)
type note struct { ID int64 ` + "`json:\"id\"`" + `; Title string ` + "`json:\"title\"`" + `; Summary *string ` + "`json:\"summary\"`" + ` }
type state struct { Version string ` + "`json:\"version\"`" + `; PID int ` + "`json:\"pid\"`" + `; Notes []note ` + "`json:\"notes\"`" + ` }
func marker(name string) error { return os.WriteFile(filepath.Join(os.Getenv("GODJ_RELOAD_CONTROL"), name), []byte(strconv.Itoa(os.Getpid())), 0600) }
func waitFile(ctx context.Context, name string) error {
 ticker := time.NewTicker(10*time.Millisecond); defer ticker.Stop()
 for { if _, err := os.Stat(filepath.Join(os.Getenv("GODJ_RELOAD_CONTROL"), name)); err == nil { return nil }; select { case <-ctx.Done(): return ctx.Err(); case <-ticker.C: } }
}
func main() {
 if len(os.Args)==2 && os.Args[1]=="leaked-worker" {
  signal.Ignore(os.Interrupt, syscall.SIGTERM)
  if marker("leaked-pid") != nil { os.Exit(2) }
  for { time.Sleep(time.Hour) }
 }
 if handled, err := compatibility.Serve(context.Background(), os.Args[1:], os.Stdout); handled { if err != nil { os.Exit(2) }; return }
 ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM); defer stop()
 if err := run(ctx); err != nil { fmt.Fprintln(os.Stderr,"notes site failed"); os.Exit(1) }
}
func run(ctx context.Context) error {
 environment, err := settings.NewEnvironment(os.Environ()); if err != nil { return err }
 selected, err := config.Database(environment); if err != nil { return err }
 version := revision.Current()
 return projectserver.Serve(ctx, os.Args[1:], projectserver.Config{
  Start: func(start context.Context) (projectserver.Runtime,error) {
   backend, err := database.Open(start, selected); if err != nil { return projectserver.Runtime{},err }
   runtime := projectserver.Runtime{Stop: func(context.Context) error { return errors.Join(backend.Close(), marker("stopped-"+strconv.Itoa(os.Getpid()))) }}
   returned := false
   defer func(){ if !returned { _ = runtime.Stop(context.WithoutCancel(start)) } }()
   // This app's startup read prepares the concrete model view. A schema
   // change that has not been explicitly migrated cannot become ready.
   startupRows, err := models.NoteObjects.Using(backend).Limit(1); if err != nil { returned=true; return runtime,err }
   if _, err := startupRows.All(start); err != nil { returned=true; return runtime,err }
   if version=="spawn-leak" {
    child:=exec.Command(os.Args[0],"leaked-worker"); child.Env=os.Environ(); child.Stdout=os.Stdout; child.Stderr=os.Stderr
    if err:=child.Start();err!=nil {returned=true;return runtime,err}
    if err:=waitFile(start,"leaked-pid");err!=nil{returned=true;return runtime,err}
    returned=true;return runtime,errors.New("private-reload-startup-marker")
   }
   if strings.HasPrefix(version,"wait-") {
    if err:=marker("waiting-"+version);err!=nil{returned=true;return runtime,err}
    if err:=waitFile(start,"release-"+version);err!=nil{returned=true;return runtime,err}
   }
   configured,err:=settings.New(settings.Definition{ProjectName:"reload-notes",InstalledApps:project.InstalledApps()});if err!=nil{returned=true;return runtime,err}
   response:=func(ctx context.Context,status int)(web.Response,error){
    rows,err:=models.NoteObjects.Using(backend).All(ctx);if err!=nil{return web.Response{},err}
    result:=state{Version:version,PID:os.Getpid(),Notes:[]note{}}
    for _,row:=range rows{item:=note{ID:row.ID,Title:row.Title};@@READ@@;result.Notes=append(result.Notes,item)}
    sort.Slice(result.Notes,func(i,j int)bool{return result.Notes[i].ID<result.Notes[j].ID})
    body,err:=json.Marshal(result);if err!=nil{return web.Response{},err};return web.NewResponse(status,http.Header{"Content-Type":{"application/json"}},body)
   }
   runtime.Application,err=web.NewApplication(web.Config{Settings:configured,Routes:[]web.Route{
    {Name:"notes:list",Method:"GET",Path:"/",Handler:func(request *web.Request)(web.Response,error){return response(request.Context(),200)}},
    {Name:"notes:create",Method:"POST",Path:"/save",Handler:func(request *web.Request)(web.Response,error){
     title:=request.HTTP().URL.Query().Get("title");if title==""||len(title)>80{return web.NewResponse(400,nil,[]byte("invalid title"))}
     if request.HTTP().URL.Query().Get("mode")=="hold" {if err:=marker("request-held");err!=nil{return web.Response{},err};if err:=waitFile(request.Context(),"release-request");err!=nil{return web.Response{},err}}
     if start.Err()!=nil{return web.Response{},errors.New("application canceled before request drain")}
     create:=models.NewNoteCreate(title);@@WRITE@@
     if _,err:=models.NoteObjects.Create(request.Context(),backend,create);err!=nil{return web.Response{},err};return response(request.Context(),201)
    }},
   }})
   returned=true;return runtime,err
  },
  Ready:func(_ context.Context,info projectserver.ListenerInfo)error{message:="notes site listening on http://"+info.Address+"\n";n,err:=io.WriteString(os.Stdout,message);if err==nil&&n!=len(message){return io.ErrShortWrite};return err},
 })
}
`
