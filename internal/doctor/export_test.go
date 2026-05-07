package doctor

// DoRegistryProbeForTest exposes doRegistryProbe for tests so they can inject
// a pre-built URL pointing at an httptest server without needing TLS.
func DoRegistryProbeForTest(name, host, probeRef, probeURL string) Check {
	return doRegistryProbe(name, host, probeRef, probeURL)
}
