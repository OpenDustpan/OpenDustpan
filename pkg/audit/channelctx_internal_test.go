package audit

import (
	"context"
	"testing"
)

// TestChannelContext proves WithChannel and ChannelFrom: a set channel comes
// back, a missing one gives the api channel, a channel that is neither api nor
// console is stored as api, and a value of the wrong type under the key is
// ignored.
func TestChannelContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // the table holds one context for each case
		want Channel
	}{
		{"console is set", WithChannel(context.Background(), ChannelConsole), ChannelConsole},
		{"api is set", WithChannel(context.Background(), ChannelAPI), ChannelAPI},
		{"no channel gives api", context.Background(), ChannelAPI},
		{"an unknown channel is stored as api", WithChannel(context.Background(), Channel("sso")), ChannelAPI},
		{"an empty channel is stored as api", WithChannel(context.Background(), Channel("")), ChannelAPI},
		{"a value of the wrong type gives api", context.WithValue(context.Background(), channelKey{}, "console"), ChannelAPI},
		{
			"the inner channel wins over an outer one",
			WithChannel(WithChannel(context.Background(), ChannelConsole), ChannelAPI),
			ChannelAPI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ChannelFrom(tt.ctx); got != tt.want {
				t.Fatalf("ChannelFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}
