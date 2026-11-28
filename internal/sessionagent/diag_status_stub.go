//go:build !e2ediag

package sessionagent

import "context"

func (s *Supervisor) runDiagLoop(context.Context) {}
