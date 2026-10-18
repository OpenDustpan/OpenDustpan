package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ErrNoCredentials reports a helper request for a host that the launcher
// has no credential for, or a request outside a tool step.
var ErrNoCredentials = errors.New("no credential for this host")

// Files of the credentials on the memory-backed volume. The launcher writes
// each with mode 0600 before a step of the role tool, and deletes the
// directory of the step when the step ends. A step of the role hook gets
// none of them.
const (
	// FileOpenTofuConfig is the OpenTofu CLI configuration that names the
	// credentials helper. It holds no credential.
	FileOpenTofuConfig = "tofu.tfrc"
	// DirPulumiHome is the Pulumi home directory of the step. It holds
	// FilePulumiCredentials.
	DirPulumiHome = "pulumi-home"
	// FilePulumiCredentials is the credentials file of the Pulumi CLI,
	// with the job token for the server.
	FilePulumiCredentials = "credentials.json"
	// FileGitConfig is the git configuration that names the git credential
	// helper. It holds no credential.
	FileGitConfig = "gitconfig"
	// DirProfile holds the credential files of the profile.
	DirProfile = "profile"
	// SocketHelper is the Unix socket on which the launcher answers its
	// helpers during a tool step. The directory has mode 0700, so only the
	// user of the step reaches it.
	SocketHelper = "helper.sock"
)

// Tool is the lowercase code of a tool, as in a unit reference.
type Tool string

// The tools whose credentials the launcher gives.
const (
	ToolOpenTofu Tool = "opentofu"
	ToolPulumi   Tool = "pulumi"
)

// Delivery is how the job token reaches a tool.
type Delivery string

// The deliveries of the job token.
const (
	// DeliveryHelper: the launcher is the credentials helper of the tool.
	// The token stays in the memory of the launcher.
	DeliveryHelper Delivery = "helper"
	// DeliveryFile: the token is in a file of mode 0600 on the
	// memory-backed volume, for the time of the step.
	DeliveryFile Delivery = "file"
)

// DeliveryFor returns how the job token reaches tool t. It returns
// ErrNoCredentials for a tool that this package does not know: its steps
// get no job token.
func DeliveryFor(t Tool) (Delivery, error) {
	switch t {
	case ToolOpenTofu:
		return DeliveryHelper, nil
	case ToolPulumi:
		return DeliveryFile, nil
	default:
		return "", fmt.Errorf("%w: tool %q", ErrNoCredentials, t)
	}
}

// OpenTofuConfig returns the OpenTofu CLI configuration of a tool step: it
// names the launcher as the credentials helper "dustpan", and nothing
// else. OpenTofu looks for the helper program HelperOpenTofu in its plugin
// directories, so the launcher links itself there, in the home directory
// of the step.
func OpenTofuConfig() []byte {
	return fmt.Appendf(nil, "credentials_helper %q {}\n", strings.TrimPrefix(HelperOpenTofu, "terraform-credentials-"))
}

// OpenTofuAnswer returns the answer of the credentials helper to the
// request "get host": {"token": token} for the host of the server, and {}
// for every other host, as the helper protocol of OpenTofu wants for a
// host with no credential. serverURL is the URL of the server.
func OpenTofuAnswer(host, serverURL, token string) ([]byte, error) {
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: server URL %q has no host", ErrUsage, serverURL)
	}

	if !strings.EqualFold(host, u.Host) || token == "" {
		return []byte("{}\n"), nil
	}

	b, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return nil, fmt.Errorf("encode the answer of the helper: %w", err)
	}

	return append(b, '\n'), nil
}

// PulumiCredentials returns the content of the credentials file of the
// Pulumi CLI for a step: the server URL as the current backend, with the
// job token.
func PulumiCredentials(serverURL, token string) ([]byte, error) {
	b, err := json.Marshal(map[string]any{
		"current":      serverURL,
		"accessTokens": map[string]string{serverURL: token},
	})
	if err != nil {
		return nil, fmt.Errorf("encode the Pulumi credentials file: %w", err)
	}

	return b, nil
}

// GitConfig returns the git configuration of a step: the credential helper
// helper, which is an absolute path or a bare git-credential- name. Git
// then starts it with no shell. With an empty helper, the configuration
// sets an empty helper list, so a step gets no credential from git
// configuration of the image.
func GitConfig(helper string) []byte {
	if helper == "" {
		return []byte("[credential]\n\thelper =\n")
	}

	name := helper
	if !path.IsAbs(helper) {
		name = strings.TrimPrefix(helper, "git-credential-")
	}

	return fmt.Appendf(nil, "[credential]\n\thelper =\n\thelper = %s\n", name)
}

// TokenSource is the port through which a helper process asks the
// launcher for a credential during a tool step. The launcher answers on
// SocketHelper, and only while the step of the role tool runs.
type TokenSource interface {
	// Token returns the job token for the host of the server. It returns
	// ErrNoCredentials for any other host, and outside a tool step.
	Token(ctx context.Context, host string) (string, error)
	// GitToken returns a short-lived read-only token of the forge
	// connection of the job, for the git credential helper of the mode
	// forge. It returns ErrNoCredentials for a host that is not a host of
	// the forge connection, and for every host in the mode none or custom.
	GitToken(ctx context.Context, host string) (string, error)
}

// Masker hides the secret values of a phase in the log of each step. The
// launcher gives it every value that it got through the credentials of the
// session. A value that a chunk boundary splits is still hidden.
type Masker interface {
	// Add adds a secret value. Values shorter than 4 bytes are not masked,
	// because they would hide common text.
	Add(value []byte)
	// Write masks p and writes it to the log, holding back the end of p
	// that could start a secret value.
	Write(p []byte) (int, error)
	// Close writes what Write held back.
	Close() error
}

// MemoryVolume is the port that prepares the credentials directory before
// the first step. On Linux, the adapter reads the type of the file system
// with statfs.
type MemoryVolume interface {
	// Prepare checks that dir is on tmpfs or ramfs, and then creates in it
	// a new directory with mode 0700 that the user of the launcher owns,
	// and returns its path. The volume itself can have another owner and
	// mode, for example an emptyDir of Kubernetes with the medium Memory.
	// It returns an error when dir is not on tmpfs or ramfs, or when the
	// new directory does not have the mode 0700 and the user of the
	// launcher as its owner after its creation. The launcher then ends with
	// ExitSandbox.
	Prepare(dir string) (string, error)
}

// StepUser is the user and the group of the processes of the steps, when
// the place lets the launcher run them as a user other than its own.
type StepUser struct {
	UID uint32
	GID uint32
}

// Isolated reports whether the steps of u run as a user other than the
// launcher user launcherUID. A zero StepUser runs the steps as the user of
// the launcher.
func (u StepUser) Isolated(launcherUID uint32) bool {
	return u.UID != 0 && u.UID != launcherUID
}
