package records

import "errors"

// The RPS/REPO RQ_MAIN records and XML forms are not recovered (rps.md I): only
// the D literals are known. This explicit error keeps callers honest instead of
// fabricating a layout.
var ErrRPSLayout = errors.New("RPS/REPO record layout is not recovered (I)")
