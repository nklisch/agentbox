package kits

// ClassifyPullStderrForTest exposes the unexported classifyPullStderr for
// table-driven unit tests in the kits_test package.
func ClassifyPullStderrForTest(stderr string, ctxErr error) PullErrorKind {
	return classifyPullStderr(stderr, ctxErr)
}
