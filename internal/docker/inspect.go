package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	mobyclient "github.com/moby/moby/client"
)

// Kind says what an ID refers to.
type Kind string

const (
	KindContainer Kind = "container"
	KindImage     Kind = "image"
)

// Inspect returns the daemon's full description of a container or image
// as indented JSON — exactly what `docker inspect` prints. It uses the raw
// response rather than re-encoding the decoded struct, so fields the
// client library doesn't know about still show up.
func (c *client) Inspect(ctx context.Context, kind Kind, id string) (string, error) {
	var raw json.RawMessage
	switch kind {
	case KindContainer:
		res, err := c.api.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
		if err != nil {
			return "", err
		}
		raw = res.Raw
	case KindImage:
		// Images return the decoded struct only; the raw body has to be
		// asked for with an option that copies it into a buffer.
		var buf bytes.Buffer
		if _, err := c.api.ImageInspect(ctx, id, mobyclient.ImageInspectWithRawResponse(&buf)); err != nil {
			return "", err
		}
		raw = buf.Bytes()
	default:
		return "", fmt.Errorf("cannot inspect a %q", kind)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return "", err
	}
	return out.String(), nil
}
