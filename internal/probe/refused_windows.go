//go:build windows

package probe

import (
	"errors"
	"syscall"
)

// wsaeconnrefused ist WSAECONNREFUSED. Unter Windows meldet Go eine Ablehnung mit dieser Nummer;
// syscall.ECONNREFUSED ist dort ein eigener Wert und passt nicht darauf, und der Fehlertext ist
// übersetzt ("… verweigerte die Verbindung") – ein Textvergleich auf "refused" ginge auf einem
// deutschen Windows also schief.
const wsaeconnrefused = syscall.Errno(10061)

func isRefused(err error) bool {
	return errors.Is(err, wsaeconnrefused) || errors.Is(err, syscall.ECONNREFUSED)
}
