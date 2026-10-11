//go:build darwin || linux

package linked

import "context"

func publishLinkedResponse(ctx context.Context, before func(), honorCancellation bool, writes *int, write func() error) error {
	if before != nil {
		before()
	}
	if honorCancellation {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	*writes++
	return write()
}
