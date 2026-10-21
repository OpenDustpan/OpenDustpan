package audit

import "context"

type channelKey struct{}

// WithChannel returns a context that carries the channel of the request that
// it serves. A service that writes an entry for work it does not request,
// such as a security entry for a refused blob key, reads it with ChannelFrom.
// A channel that is not api or console is stored as api.
func WithChannel(ctx context.Context, c Channel) context.Context {
	if c != ChannelConsole {
		c = ChannelAPI
	}

	return context.WithValue(ctx, channelKey{}, c)
}

// ChannelFrom returns the channel that WithChannel stored in ctx, or
// ChannelAPI when ctx has none.
func ChannelFrom(ctx context.Context) Channel {
	if c, ok := ctx.Value(channelKey{}).(Channel); ok {
		return c
	}

	return ChannelAPI
}
