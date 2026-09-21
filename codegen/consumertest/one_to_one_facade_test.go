package codegen_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/onetoonefixture"
	"github.com/progresshans/godj/schema"
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

func TestGeneratedOneToOneReverseAssignmentStagesBothWrappers(t *testing.T) {
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-one-to-one-assignment"
	spec.Project.ImportPath = module + "/project"
	for i := range spec.Apps {
		spec.Apps[i].Package.ImportPath = module + "/" + spec.Apps[i].Alias
	}
	nodes, err := schema.Build(schema.Definition{AppLabel: "nodes", Models: []schema.Model{{Name: "node", GoName: "Node", Fields: []schema.Field{
		schema.OneToOne("next", "NextID", schema.Target("nodes", "node"), schema.RelatedName("previous"), schema.SetNull, schema.Nullable()),
		schema.CharField("label", "Label", 50),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Apps = append(spec.Apps, codegen.AppSpec{Alias: "nodes", Package: codegen.PackageSpec{PackageName: "nodes", ImportPath: module + "/nodes", Directory: "nodes"}, Schema: nodes})
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	writeGeneratedTestFile(t, root, "project/assignment_test.go", []byte(`package project
import (
 "context"
 "errors"
 "testing"
 "example.com/godj-one-to-one-assignment/nodes"
 "example.com/godj-one-to-one-assignment/reports"
 "example.com/godj-one-to-one-assignment/tickets"
 "github.com/progresshans/godj/db"
 "github.com/progresshans/godj/query"
)
type assignmentNoIO struct{ calls int }
func(b *assignmentNoIO)Query(context.Context,query.Plan)(db.Rows,error){b.calls++;return nil,errors.New("unexpected query")}
func(b *assignmentNoIO)Insert(context.Context,query.InsertPlan)(int64,error){b.calls++;return 0,errors.New("unexpected insert")}
func(b *assignmentNoIO)Update(context.Context,query.UpdatePlan)(int64,error){b.calls++;return 0,errors.New("unexpected update")}
func(b *assignmentNoIO)Delete(context.Context,query.DeletePlan)(int64,error){b.calls++;return 0,errors.New("unexpected delete")}
func TestReverseAssignmentStagesBeforePublishing(t *testing.T){
 b:=&assignmentNoIO{};models,err:=Using(b);if err!=nil{t.Fatal(err)}
 owner,err:=models.TicketsTicket.New(tickets.NewTicketWithID(7));if err!=nil{t.Fatal(err)}
 rawReport:=reports.NewReportWithID(4);rawReport.TicketID=7
 report,err:=models.ReportsReport.New(rawReport);if err!=nil{t.Fatal(err)}
 report,err=report.WithTicket(owner);if err!=nil{t.Fatal(err)}
 rawCertificate:=reports.NewCertificateWithID(2);rawCertificate.ReportID=4
 certificate,err:=models.ReportsCertificate.New(rawCertificate);if err!=nil{t.Fatal(err)}
 report.TicketID=8
 object,cache,snapshot:=report.object,report.ticketCache,report.ticketScalarSnapshot
 childObject,childCache:=certificate.object,certificate.reportCache
 certificate.reportCache=nil
 if err=report.SetCertificate(certificate);!errors.Is(err,&query.Error{Code:query.CodeInvalidPlan}){t.Fatal("corrupt child cache accepted",err)}
 if report.object!=object||report.ticketCache!=cache||report.ticketScalarSnapshot!=snapshot||report.TicketID!=8||certificate.object!=childObject||certificate.ReportID!=4{t.Fatal("failed setter partially published staged reconciliation")}
 certificate.reportCache=childCache
 if err=report.SetCertificate(certificate);err!=nil{t.Fatal(err)}
 if report.ticketScalarSnapshot!=8||report.object==object||report.ticketCache==cache{t.Fatal("successful setter did not publish source reconciliation")}
 target,found,err:=report.Certificate(t.Context());if err!=nil||!found||target!=certificate{t.Fatal("reverse identity",err)}
 parent,err:=certificate.Report(t.Context());if err!=nil||parent!=report{t.Fatal("forward identity",err)}
 canceled,cancel:=context.WithCancel(t.Context());cancel()
 if _,_,err=report.Certificate(canceled);!errors.Is(err,context.Canceled){t.Fatal("warm relation ignored cancellation",err)}
 if err=report.SetCertificate(nil);err!=nil{t.Fatal(err)}
 if certificate.ReportID!=0||certificate.reportScalarPresent{t.Fatal("required child clear retained key presence")}
 if _,err=certificate.Unwrap();!errors.Is(err,&query.Error{Code:query.CodeRequiredField}){t.Fatal("cleared required FK escaped through raw model",err)}
 if err=certificate.Save(t.Context());!errors.Is(err,&query.Error{Code:query.CodeRequiredField}){t.Fatal("cleared required FK reached storage",err)}
 if b.calls!=0{t.Fatal("memory assignment did I/O",b.calls)}
}
func TestReverseSelfAssignmentPreservesBothEdges(t *testing.T){
 b:=&assignmentNoIO{};models,err:=Using(b);if err!=nil{t.Fatal(err)}
 node,err:=models.NodesNode.New(nodes.NewNodeWithID(1));if err!=nil{t.Fatal(err)}
 if err=node.SetPrevious(node);err!=nil{t.Fatal(err)}
 previous,found,err:=node.Previous(t.Context());if err!=nil||!found||previous!=node{t.Fatal("self reverse",err)}
 next,found,err:=node.Next(t.Context());if err!=nil||!found||next!=node||node.NextID==nil||*node.NextID!=1{t.Fatal("self forward",err)}
 if err=node.SetPrevious(nil);err!=nil{t.Fatal(err)}
 previous,found,err=node.Previous(t.Context());if err!=nil||found||previous!=nil{t.Fatal("self clear reverse",err)}
 next,found,err=node.Next(t.Context());if err!=nil||found||next!=nil||node.NextID!=nil{t.Fatal("self clear forward",err)}
 other,err:=models.NodesNode.New(nodes.NewNodeWithID(2));if err!=nil{t.Fatal(err)}
 if err=node.SetPrevious(other);err!=nil{t.Fatal(err)}
 next,found,err=other.Next(t.Context());if err!=nil||!found||next!=node{t.Fatal("same type different pointers",err)}
 previous,found,err=node.Previous(t.Context());if err!=nil||!found||previous!=other||node.NextID!=nil{t.Fatal("same type owner edges",err)}
 if b.calls!=0{t.Fatal("self assignment did I/O",b.calls)}
}
`))
	compileGeneratedRelationFacadeUniverse(t, root)
}
