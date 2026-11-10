package assets

import "context"

// Progress describes a stage of first-time Apple SAP asset provisioning.
type Progress struct {
	Stage   string
	Message string
	Current int64
	Total   int64
}

// ProgressFunc receives throttled progress updates during Load.
type ProgressFunc func(Progress)

type progressKey struct{}

// WithProgress attaches a progress reporter to ctx for Load / resolveRemoteImage.
func WithProgress(ctx context.Context, reporter ProgressFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, progressKey{}, reporter)
}

func progressFrom(ctx context.Context) ProgressFunc {
	if ctx == nil {
		return nil
	}
	reporter, _ := ctx.Value(progressKey{}).(ProgressFunc)
	return reporter
}

func reportProgress(ctx context.Context, p Progress) {
	if reporter := progressFrom(ctx); reporter != nil {
		reporter(p)
	}
}
