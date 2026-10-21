package chat_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
)

func TestPlatformValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		platform chat.Platform
		want     bool
	}{
		{chat.Slack, true},
		{chat.Teams, true},
		{chat.Mattermost, true},
		{chat.Matrix, true},
		{"", false},
		{"Slack", false},
		{"slack ", false},
		{"discord", false},
	}

	for _, tc := range tests {
		if got := tc.platform.Valid(); got != tc.want {
			t.Errorf("Platform(%q).Valid() = %v, want %v", tc.platform, got, tc.want)
		}
	}

	if got := len(chat.Platforms()); got != 4 {
		t.Errorf("len(Platforms()) = %d, want 4", got)
	}
}

func TestContentValidate(t *testing.T) {
	t.Parallel()

	valid := func() chat.Content {
		return chat.Content{
			NotificationID: "ntf_1",
			Product:        "Acme Deploy",
			Title:          "Job failed",
			Summary:        "The job failed.",
			Severity:       notify.SeverityCritical,
			Fields:         []chat.Field{{Label: "Namespace", Value: "shop"}},
			Links: []notify.Link{
				{Title: "Open", URL: "https://console.example/jobs/1", Kind: notify.LinkConsole},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(c *chat.Content)
		ok     bool
	}{
		{"valid", func(*chat.Content) {}, true},
		{"empty summary", func(c *chat.Content) { c.Summary = "" }, true},
		{"line feed in the summary", func(c *chat.Content) { c.Summary = "one\ntwo" }, true},
		{"no notification ID", func(c *chat.Content) { c.NotificationID = "" }, false},
		{"notification ID with a slash", func(c *chat.Content) { c.NotificationID = "a/b" }, false},
		{
			"long notification ID",
			func(c *chat.Content) { c.NotificationID = strings.Repeat("n", chat.MaxIDLen+1) },
			false,
		},
		{"CR in the title", func(c *chat.Content) { c.Title = "a\rb" }, false},
		{"NUL in the summary", func(c *chat.Content) { c.Summary = "a\x00b" }, false},
		{"tab in a label", func(c *chat.Content) { c.Fields[0].Label = "a\tb" }, false},
		{"RLO in a value", func(c *chat.Content) { c.Fields[0].Value = "a\u202eb" }, false},
		{"isolate in the product", func(c *chat.Content) { c.Product = "a\u2066b" }, false},
		{"C1 control in a link title", func(c *chat.Content) { c.Links[0].Title = "a\u0085b" }, false},
		{"invalid UTF-8 in the title", func(c *chat.Content) { c.Title = "a\xffb" }, false},
		{"no product", func(c *chat.Content) { c.Product = "" }, false},
		{"long product", func(c *chat.Content) { c.Product = strings.Repeat("p", chat.MaxProductLen+1) }, false},
		{"no title", func(c *chat.Content) { c.Title = "" }, false},
		{"long title", func(c *chat.Content) { c.Title = strings.Repeat("t", chat.MaxTitleLen+1) }, false},
		{"long summary", func(c *chat.Content) { c.Summary = strings.Repeat("s", notify.MaxSummaryLen+1) }, false},
		{"zero severity", func(c *chat.Content) { c.Severity = 0 }, false},
		{"unknown severity", func(c *chat.Content) { c.Severity = notify.SeverityCritical + 1 }, false},
		{"empty label", func(c *chat.Content) { c.Fields[0].Label = "" }, false},
		{"long value", func(c *chat.Content) { c.Fields[0].Value = strings.Repeat("v", chat.MaxFieldLen+1) }, false},
		{"too many fields", func(c *chat.Content) { c.Fields = make([]chat.Field, chat.MaxFields+1) }, false},
		{"too many links", func(c *chat.Content) { c.Links = make([]notify.Link, notify.MaxLinks+1) }, false},
		{"link with no title", func(c *chat.Content) { c.Links[0].Title = "" }, false},
		{"javascript link", func(c *chat.Content) { c.Links[0].URL = "javascript:alert(1)" }, false},
		{"file link", func(c *chat.Content) { c.Links[0].URL = "file:///etc/passwd" }, false},
		{
			"link with user information",
			func(c *chat.Content) { c.Links[0].URL = "https://u:p@console.example/" },
			false,
		},
		{"link with no host", func(c *chat.Content) { c.Links[0].URL = "https:///x" }, false},
		{"link too long", func(c *chat.Content) {
			c.Links[0].URL = "https://c.example/" + strings.Repeat("u", notify.MaxLinkURLLen)
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := valid()
			tc.mutate(&c)

			err := c.Validate()
			switch {
			case tc.ok && err != nil:
				t.Fatalf("Validate = %v, want nil", err)
			case !tc.ok && !errors.Is(err, notify.ErrInvalid):
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			default:
			}
		})
	}
}

// TestSinkPlatformsMatch keeps the platform list of the sink in the notify
// package equal to the platforms of this package, in the same order.
func TestSinkPlatformsMatch(t *testing.T) {
	t.Parallel()

	want := chat.Platforms()
	got := notify.ChatPlatforms()

	if len(got) != len(want) {
		t.Fatalf("notify.ChatPlatforms = %v, want the %d platforms of chat.Platforms", got, len(want))
	}

	for i := range want {
		if got[i] != string(want[i]) {
			t.Fatalf("notify.ChatPlatforms[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
