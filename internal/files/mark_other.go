//go:build !windows && !darwin

package files

// markFromInternet does nothing: there is no common download marker here.
func markFromInternet(string) {}
