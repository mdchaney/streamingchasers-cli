package cli

import "strconv"

func strconvFormat(n float64) string { return strconv.FormatFloat(n, 'f', 1, 64) }
