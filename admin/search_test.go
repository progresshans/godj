package admin

import (
	"context"
	"testing"
)

func TestModelWithoutSearchRejectsSearchBeforeCallback(t *testing.T) {
	config := validRegistryConfig(t)
	config.SearchFields = nil
	calls := 0
	config.List = func(_ context.Context, request ListRequest) (Page[registryArticle], error) {
		calls++
		return Page[registryArticle]{Offset: request.Offset, Limit: request.Limit}, nil
	}
	builder := NewBuilder(mustApps(t))
	if err := RegisterModel(builder, config); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.models[0].list(context.Background(), mustPrincipal(t), ListRequest{}); err != nil || calls != 1 {
		t.Fatal("disabled search prevents listing", err)
	}
	if _, err := registry.models[0].list(context.Background(), mustPrincipal(t), ListRequest{Search: "cannot apply"}); errorCode(err) != "unavailable" || calls != 1 {
		t.Fatal("disabled search silently reached list callback", err)
	}
	if len(registry.All()[0].SearchFields) != 0 {
		t.Fatal("disabled search was replaced")
	}
}
