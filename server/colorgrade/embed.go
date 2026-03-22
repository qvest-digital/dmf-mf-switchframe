package colorgrade

import "embed"

//go:embed looks/*.cube
var builtinLooksFS embed.FS
