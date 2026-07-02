package facade

import (
	"strings"
	"testing"

	"github.com/pax-oss/paxl/internal/model"
)

func TestInjectCapsuleRequestFromMemoryRoute(t *testing.T) {
	cases := []struct {
		name       string
		req        *QueueMemoryDeliveryRequest
		want       *InjectCapsuleRequest
		wantErr    bool
		wantErrSub string
	}{
		{
			name:    "nil request",
			wantErr: true,
		},
		{
			name:       "nil route",
			req:        &QueueMemoryDeliveryRequest{CapsuleID: "kcap_1"},
			wantErr:    true,
			wantErrSub: "route is required",
		},
		{
			name: "session",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route: &LocalMemoryRoute{
					Kind:      LocalMemoryRouteKindSession,
					Agent:     model.AgentNameCodex,
					SessionID: "codex:target",
				},
				ActionItems: []string{"review"},
			},
			want: &InjectCapsuleRequest{
				CapsuleID:       "kcap_1",
				TargetSessionID: "codex:target",
				Agent:           model.AgentNameCodex,
				ActionItems:     []string{"review"},
			},
		},
		{
			name: "new session",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route: &LocalMemoryRoute{
					Kind:  LocalMemoryRouteKindNewSession,
					Agent: model.AgentNameClaude,
				},
			},
			want: &InjectCapsuleRequest{
				CapsuleID:  "kcap_1",
				Agent:      model.AgentNameClaude,
				NewSession: true,
			},
		},
		{
			name: "any",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route:     &LocalMemoryRoute{Kind: LocalMemoryRouteKindAny},
			},
			want: &InjectCapsuleRequest{
				CapsuleID: "kcap_1",
				MatchType: "any",
			},
		},
		{
			name: "keyword",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route: &LocalMemoryRoute{
					Kind:    LocalMemoryRouteKindKeyword,
					Agent:   model.AgentNameClaude,
					Keyword: "handoff",
				},
			},
			want: &InjectCapsuleRequest{
				CapsuleID:  "kcap_1",
				Agent:      model.AgentNameClaude,
				MatchType:  "keyword",
				MatchValue: "handoff",
			},
		},
		{
			name: "project",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route: &LocalMemoryRoute{
					Kind:    LocalMemoryRouteKindProject,
					Project: "paxl",
				},
			},
			want: &InjectCapsuleRequest{
				CapsuleID:  "kcap_1",
				MatchType:  "project",
				MatchValue: "paxl",
			},
		},
		{
			name: "unknown",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route:     &LocalMemoryRoute{Kind: LocalMemoryRouteKindUnknown},
			},
			wantErr:    true,
			wantErrSub: "route kind is required",
		},
		{
			name: "unsupported",
			req: &QueueMemoryDeliveryRequest{
				CapsuleID: "kcap_1",
				Route:     &LocalMemoryRoute{Kind: "agent"},
			},
			wantErr:    true,
			wantErrSub: "unsupported route kind",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := injectCapsuleRequestFromMemoryRoute(tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatal("injectCapsuleRequestFromMemoryRoute() error = nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("injectCapsuleRequestFromMemoryRoute() error = %v", err)
			}
			assertInjectCapsuleRequest(t, got, tc.want)
		})
	}
}

func TestShareSessionMemoryRoute(t *testing.T) {
	explicit := &LocalMemoryRoute{Kind: LocalMemoryRouteKindAny}
	got := shareSessionMemoryRoute(&ShareSessionMemoryRequest{Route: explicit})
	if got != explicit {
		t.Fatal("shareSessionMemoryRoute() did not preserve explicit route")
	}
	cases := []struct {
		name string
		req  *ShareSessionMemoryRequest
		want *LocalMemoryRoute
	}{
		{
			name: "new session",
			req:  &ShareSessionMemoryRequest{NewSession: true, TargetAgent: model.AgentNameClaude},
			want: &LocalMemoryRoute{
				Kind:  LocalMemoryRouteKindNewSession,
				Agent: model.AgentNameClaude,
			},
		},
		{
			name: "target session",
			req: &ShareSessionMemoryRequest{
				TargetAgent:     model.AgentNameCodex,
				TargetSessionID: "codex:target",
			},
			want: &LocalMemoryRoute{
				Kind:      LocalMemoryRouteKindSession,
				Agent:     model.AgentNameCodex,
				SessionID: "codex:target",
			},
		},
		{
			name: "legacy match",
			req: &ShareSessionMemoryRequest{
				TargetAgent: model.AgentNameClaude,
				MatchType:   "project",
				MatchValue:  "paxl",
			},
			want: &LocalMemoryRoute{
				Kind:    LocalMemoryRouteKindProject,
				Agent:   model.AgentNameClaude,
				Keyword: "paxl",
				Project: "paxl",
			},
		},
		{
			name: "none",
			req:  &ShareSessionMemoryRequest{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shareSessionMemoryRoute(tc.req)
			assertLocalMemoryRoute(t, got, tc.want)
		})
	}
}

func assertInjectCapsuleRequest(
	t *testing.T,
	got *InjectCapsuleRequest,
	want *InjectCapsuleRequest,
) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Fatalf("request = %#v, want %#v", got, want)
		}
		return
	}
	if got.CapsuleID != want.CapsuleID ||
		got.TargetSessionID != want.TargetSessionID ||
		got.Agent != want.Agent ||
		got.NewSession != want.NewSession ||
		got.MatchType != want.MatchType ||
		got.MatchValue != want.MatchValue {
		t.Fatalf("request = %#v, want %#v", got, want)
	}
	if len(got.ActionItems) != len(want.ActionItems) {
		t.Fatalf("action items = %#v, want %#v", got.ActionItems, want.ActionItems)
	}
	for index := range got.ActionItems {
		if got.ActionItems[index] != want.ActionItems[index] {
			t.Fatalf("action items = %#v, want %#v", got.ActionItems, want.ActionItems)
		}
	}
}

func assertLocalMemoryRoute(t *testing.T, got *LocalMemoryRoute, want *LocalMemoryRoute) {
	t.Helper()
	if got == nil || want == nil {
		if got != want {
			t.Fatalf("route = %#v, want %#v", got, want)
		}
		return
	}
	if got.Kind != want.Kind ||
		got.Agent != want.Agent ||
		got.SessionID != want.SessionID ||
		got.Keyword != want.Keyword ||
		got.Project != want.Project {
		t.Fatalf("route = %#v, want %#v", got, want)
	}
}
