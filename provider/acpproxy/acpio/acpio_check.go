package acpio

import "github.com/nisimpson/bond/provider/acpproxy"

// Compile-time interface checks ensuring acpio types satisfy acpproxy interfaces.
var _ acpproxy.ReadWriter = (*Transport)(nil)
var _ acpproxy.ReadWriter = (*StdioProcess)(nil)
var _ acpproxy.Resettable = (*StdioProcess)(nil)
