package unicorn

import (
	"context"
	"fmt"

	"github.com/ebitengine/purego"
)

// iOS builds statically link Unicorn because the desktop runtime artifacts
// cannot be loaded on iOS. app/build.sh force-loads app/build_unicorn_ios.sh output with -export_dynamic so purego can dlsym it.
func openLibrary(ctx context.Context) (library, error) {
	if err := ctx.Err(); err != nil {
		return library{}, fmt.Errorf("load Unicorn library: %w", err)
	}

	if _, err := purego.Dlsym(purego.RTLD_DEFAULT, "uc_version"); err != nil {
		return library{}, fmt.Errorf("unicorn is not linked; build with app/build_unicorn_ios.sh (see app/build.sh): %w", err)
	}

	return library{handle: purego.RTLD_DEFAULT, close: func() error { return nil }}, nil
}
