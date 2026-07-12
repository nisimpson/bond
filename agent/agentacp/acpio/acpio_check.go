package acpio

import "github.com/nisimpson/bond/agent/agentacp"

// Compile-time interface checks ensuring acpio types satisfy agentacp interfaces.
var _ agentacp.ReadWriter = (*Transport)(nil)
var _ agentacp.ReadWriter = (*StdioProcess)(nil)
var _ agentacp.Resettable = (*StdioProcess)(nil)
