package main

import (
	"github.com/gagliardetto/anchor-go/idl"
)

const GeneratedHeader = generatedHeader

type ResolutionSpec struct {
	Dependencies []string
	Source       string
	Target       int
}

func BatchName(s string) string {
	return batchName(s)
}

func ExportedName(s string) string {
	return exportedName(s)
}

func Generate(idlPath string) (map[string][]byte, error) {
	p, err := generate(options{idlPath: idlPath})
	if err != nil {
		return nil, err
	}

	return p.files, nil
}

func OrderResolutionTargets(instruction string, accounts []string, specs []ResolutionSpec) ([]int, error) {
	b := &builder{ix: &idl.IdlInstruction{Name: instruction}}
	for _, name := range accounts {
		b.accounts = append(b.accounts, builderAccount{acc: idl.IdlInstructionAccount{Name: name}})
	}

	resolutions := make([]resolution, 0, len(specs))
	for _, s := range specs {
		resolutions = append(resolutions, resolution{target: s.Target, source: s.Source, dependencies: s.Dependencies})
	}

	ordered, err := orderResolutions(b, resolutions)
	if err != nil {
		return nil, err
	}

	targets := make([]int, 0, len(ordered))
	for _, r := range ordered {
		targets = append(targets, r.target)
	}

	return targets, nil
}

func RenameUpstream(files map[string][]byte) error {
	return renameUpstream(files)
}

func RenderBuildersWithoutInstructions(idlPath string) (string, error) {
	p, err := generate(options{idlPath: idlPath})
	if err != nil {
		return "", err
	}

	p.doc.Instructions = nil

	src, err := renderBuilders(p)

	return string(src), err
}

func RewriteDiscriminators(src []byte) ([]byte, error) {
	return applyRewrites(src, rewriteDiscriminators)
}

func RewriteHelperCalls(src []byte) ([]byte, error) {
	return applyRewrites(src, rewriteHelperCalls)
}

func RewriteImports(src []byte) ([]byte, error) {
	return applyRewrites(src, rewriteImports)
}

func RewriteSolanaAlias(src []byte) ([]byte, error) {
	return applyRewrites(src, rewriteSolanaAlias)
}

func Run(idlPath, out string) error {
	return run(options{idlPath: idlPath, out: out})
}

func UnexportedName(s string) string {
	return unexportedName(s)
}
