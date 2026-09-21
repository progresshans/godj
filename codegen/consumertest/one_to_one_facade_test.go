package codegen_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/onetoonefixture"
)

func TestGeneratedOneToOneFacadePreservesIntermediateTypes(t *testing.T) {
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-one-to-one-facade"
	spec.Project.ImportPath = module + "/project"
	for i := range spec.Apps {
		spec.Apps[i].Package.ImportPath = module + "/" + spec.Apps[i].Alias
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	source := []byte(`package consumer
import (
 "context"
 "example.com/godj-one-to-one-facade/project"
)
func compileGraph(ctx context.Context,models project.Models)error{
 query:=models.TicketsTicket.SelectRelated(models.TicketsTicket.Related.Report.WithChildren(models.ReportsReport.Related.Ticket.WithChildren(models.TicketsTicket.Related.Review)))
 values,err:=query.All(ctx);if err!=nil{return err}
 for _,owner:=range values{child,present,err:=owner.Report(ctx);if err!=nil{return err};if present{parent,err:=child.Ticket(ctx);if err!=nil{return err};_,_,err=parent.Review(ctx);if err!=nil{return err}}}
 return nil
}
`)
	writeGeneratedTestFile(t, root, "consumer/consumer.go", source)
	if output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./consumer").CombinedOutput(); err != nil {
		t.Fatalf("valid typed facade failed: %v\n%s", err, output)
	}
	original := []byte("models.ReportsReport.Related.Ticket.WithChildren(models.TicketsTicket.Related.Review)")
	if bytes.Count(source, original) != 1 {
		t.Fatal("negative-control input is ambiguous")
	}
	invalid := bytes.Replace(source, original, []byte("models.TicketsTicket.Related.Review"), 1)
	writeGeneratedTestFile(t, root, "consumer/consumer.go", invalid)
	output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./consumer").CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("cannot use")) || !bytes.Contains(output, []byte("WithChildren")) || !bytes.Contains(output, []byte("relationFacadeSelectionInput[reports.Report]")) {
		t.Fatalf("wrong intermediate model was not rejected: %v\n%s", err, output)
	}
}
