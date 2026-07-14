// Package tui implements the interactive terminal UI that wfm shows when it is
// run without a subcommand. It talks to the same gRPC services as the CLI over
// a caller-supplied connection, so it works against an in-process, unix-socket,
// or remote server transparently.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/grpc"
)

// scanTimeout bounds a single access-point scan RPC.
const scanTimeout = 30 * time.Second

// Run starts the interactive UI and blocks until the user quits. ctx carries the
// request-scoped otx so RPCs issued by the UI are traced like any other; cc is a
// live client connection to the wifi services.
func Run(ctx context.Context, cc grpc.ClientConnInterface) error {
	m := newModel(ctx, cc)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
