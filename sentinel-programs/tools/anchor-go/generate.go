package main

import (
	"bytes"
	"fmt"

	"github.com/gagliardetto/anchor-go/generator"
	"github.com/gagliardetto/anchor-go/idl"
	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
)

const modulePath = "github.com/sentinel-official/sentinel-programs/clients/go"

var skippedUpstreamFiles = map[string]bool{"errors.go": true, "fetchers.go": true, "tests_test.go": true}

var stages = []stage{
	{
		name:   "client.go",
		render: renderClient,
	},
	{
		name:   "builders.go",
		render: renderBuilders,
	},
	{
		name:   "resolvers.go",
		render: renderResolvers,
	},
	{
		name:   "fetch.go",
		render: renderFetch,
	},
	{
		name:   "fetch_pda.go",
		render: renderFetchPDAs,
	},
	{
		name:   "logs.go",
		render: renderLogs,
	},
	{
		name:   "names.go",
		render: renderNames,
	},
	{
		name:   "commands.go",
		render: renderCommands,
	},
	{
		name:   "filters.go",
		render: renderFilters,
	},
	{
		name:   "addresses.go",
		render: renderAddresses,
	},
	{
		name:   "pdas.go",
		render: renderPDAs,
	},
	{
		name:   "helpers.go",
		render: renderHelpers,
	},
	{
		name:   "errors.go",
		render: renderErrors,
	},
}

type options struct {
	idlPath   string
	out       string
	pkg       string
	programID string
}

type program struct {
	doc        *idl.Idl
	files      map[string][]byte
	idlAddress *solana.PublicKey
	pkg        string
}

type stage struct {
	name   string
	render func(*program) ([]byte, error)
}

func generate(o options) (*program, error) {
	doc, err := idl.ParseFromFilepath(o.idlPath)
	if err != nil {
		return nil, err
	}

	if err := validateTypes(doc); err != nil {
		return nil, err
	}

	if err := validateAccounts(doc); err != nil {
		return nil, err
	}

	if err := doc.Validate(); err != nil {
		return nil, err
	}

	var idlAddress *solana.PublicKey
	if doc.Address != nil {
		address := *doc.Address
		idlAddress = &address
	}

	if o.programID != "" {
		id, err := solana.PublicKeyFromBase58(o.programID)
		if err != nil {
			return nil, fmt.Errorf("program-id: %w", err)
		}

		doc.Address = &id
	}

	pkg := o.pkg
	if pkg == "" {
		pkg = bin.ToSnakeForSighash(doc.Metadata.Name)
	}

	p := &program{
		doc:        doc,
		pkg:        pkg,
		idlAddress: idlAddress,
	}
	files, err := renderUpstream(doc, pkg)
	if err != nil {
		return nil, err
	}

	if err := renameUpstream(files); err != nil {
		return nil, err
	}

	if err := addInstructionDecoder(p, files); err != nil {
		return nil, err
	}

	for _, s := range stages {
		src, err := s.render(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}

		files[s.name] = src
	}

	if err := validateCommandNames(files); err != nil {
		return nil, err
	}

	for name, src := range files {
		out, err := finish(name, src)
		if err != nil {
			return nil, err
		}

		files[name] = out
	}

	p.files = files

	return p, nil
}

func renderUpstream(doc *idl.Idl, pkg string) (map[string][]byte, error) {
	opts := &generator.GeneratorOptions{
		Package:     pkg,
		ProgramName: pkg,
		ModPath:     modulePath + "/" + pkg,
		ProgramId:   doc.Address,
		SkipGoMod:   true,
	}

	out, err := generator.NewGenerator(doc, opts).Generate()
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{}

	for _, f := range out.Files {
		if skippedUpstreamFiles[f.Name] {
			continue
		}

		var b bytes.Buffer
		if err := f.File.Render(&b); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}

		files[f.Name] = b.Bytes()
	}

	return files, nil
}

func run(o options) error {
	p, err := generate(o)
	if err != nil {
		return err
	}

	return write(o.out, p.files)
}
