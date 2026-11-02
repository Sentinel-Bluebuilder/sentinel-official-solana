package leftover

func NewRemoverForTest(exists func(string) (bool, error), goos string, exec execFunc) Remover {
	return Remover{
		exec:   exec,
		exists: exists,
		goos:   goos,
	}
}

func RemoveHint(dir string, err error) string { return removeHint(dir, err) }
