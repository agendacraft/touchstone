package input

import (
	"context"
	"errors"
	"io"
	"os"
)

// Read retains at most limit bytes but counts the complete input for an
// explicit incomplete result. Cancellation returns without waiting on stdin.
func Read(ctx context.Context, name string, in io.Reader, limit int, ignore []string) (Diff, error) {
	type result struct {
		diff Diff
		err  error
	}
	done := make(chan result, 1)
	go func() {
		diff, err := read(name, in, limit, ignore)
		done <- result{diff, err}
	}()
	select {
	case result := <-done:
		return result.diff, result.err
	case <-ctx.Done():
		return Diff{}, ctx.Err()
	}
}

func read(name string, in io.Reader, limit int, ignore []string) (Diff, error) {
	if name != "-" {
		file, err := os.Open(name)
		if err != nil {
			return Diff{}, errors.New("cannot open diff file")
		}
		defer func() { _ = file.Close() }()
		in = file
	}
	data, err := io.ReadAll(io.LimitReader(in, int64(limit)))
	if err != nil {
		return Diff{}, errors.New("cannot read diff")
	}
	extra, err := io.Copy(io.Discard, in)
	if err != nil {
		return Diff{}, errors.New("cannot read diff")
	}
	size := int64(len(data)) + extra
	if extra > 0 {
		return Diff{Bytes: size, TooLarge: true}, nil
	}
	return Filter(data, ignore)
}
