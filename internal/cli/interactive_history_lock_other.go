//go:build !unix

package cli

func lockPromptHistory(string) (func(), error) {
	return func() {}, nil
}

func lockPromptHistoryIfPresent(string) (func(), error) {
	return func() {}, nil
}
