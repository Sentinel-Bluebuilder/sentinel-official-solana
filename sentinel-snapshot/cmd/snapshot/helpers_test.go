package main

func anchorFlags() []string {
	return []string{
		"--header", "/d/h.json",
		"--commit", "/d/c.json",
		"--validator-set", "/d/validator-set.json",
		"--chain-id", "sentinelhub-2",
	}
}
