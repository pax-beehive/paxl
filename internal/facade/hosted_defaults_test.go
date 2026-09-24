package facade

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostedDefaultsUsePaxWorkspaceAPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "manager login defaults to the PaxWorkspace API",
			got:  DefaultManagerURL,
			want: "https://api.paxworkspace.net",
		},
		{
			name: "self update defaults to the PaxWorkspace artifact resolver",
			got:  DefaultUpdateResolverURL,
			want: "https://api.paxworkspace.net/api/v1/public/artifacts/download",
		},
		{
			name: "daemon install defaults to the PaxWorkspace paxd resolver",
			got:  DefaultDaemonResolverURL,
			want: "https://api.paxworkspace.net/api/v1/public/paxd/download",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.got)
		})
	}
}
