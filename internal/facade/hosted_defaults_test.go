package facade

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostedDefaultsUseLakewardAPI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "manager login defaults to the Lakeward API",
			got:  DefaultManagerURL,
			want: "https://api.lakeward.net",
		},
		{
			name: "self update defaults to the Lakeward artifact resolver",
			got:  DefaultUpdateResolverURL,
			want: "https://api.lakeward.net/api/v1/public/artifacts/download",
		},
		{
			name: "daemon install defaults to the Lakeward paxd resolver",
			got:  DefaultDaemonResolverURL,
			want: "https://api.lakeward.net/api/v1/public/paxd/download",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.got)
		})
	}
}
