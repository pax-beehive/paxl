package facade

// Windows retains its existing replacement semantics; daemon upgrades are unsupported.
func LockExecutableUpdate(string) (func(), error) { return func() {}, nil }
