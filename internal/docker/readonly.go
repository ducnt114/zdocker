package docker

import (
	"context"
	"errors"
)

// ErrReadOnly is returned by every write operation of a ReadOnly client.
var ErrReadOnly = errors.New("zdocker runs with --read-only: changes are disabled")

// ReadOnly wraps c so that every operation that would change something
// (start, stop, remove…) fails with ErrReadOnly without reaching the
// daemon. Reads are passed through.
//
// Embedding the interface forwards all methods; the write ones are then
// overridden. A new write method added to Client would be forwarded too —
// TestEveryMethodIsClassified exists to catch exactly that.
func ReadOnly(c Client) Client { return readOnly{c} }

type readOnly struct{ Client }

func (readOnly) StartContainer(context.Context, string) error        { return ErrReadOnly }
func (readOnly) StopContainer(context.Context, string) error         { return ErrReadOnly }
func (readOnly) RestartContainer(context.Context, string) error      { return ErrReadOnly }
func (readOnly) RemoveContainer(context.Context, string, bool) error { return ErrReadOnly }
func (readOnly) RemoveImage(context.Context, string, bool) error     { return ErrReadOnly }
