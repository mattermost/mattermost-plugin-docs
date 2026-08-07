// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Command genimportbundle writes a valid mmetl Confluence v2 bundle ZIP for manually exercising the
// Docs import API without running mmetl. It is a development aid, not part of the plugin binary; the
// bundle it writes is built by internal/importfixture, the same package the import tests use, so a
// hand-run check and the automated tests always agree on the fixture shape.
//
//	go run ./server/cmd/genimportbundle -out /tmp/bundle.zip
//	go run ./server/cmd/genimportbundle -out /tmp/bundle.zip -pages 12 -with-findings
//	go run ./server/cmd/genimportbundle -out /tmp/broken.zip -corrupt count-mismatch
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-docs/server/internal/importfixture"
)

func main() {
	out := flag.String("out", "", "path of the bundle ZIP to write (required)")
	pages := flag.Int("pages", 3, "number of pages to emit (the first is the root, the rest are its children)")
	spaceKey := flag.String("space-key", "DOCS", "Confluence space key")
	spaceName := flag.String("space-name", "Docs", "Confluence space name")
	orgID := flag.String("organization-id", "", "optional Confluence organization id")
	team := flag.String("team", "myteam", "advisory target team recorded in the bundle (never used to route the import)")
	revision := flag.Int("revision", 0, "when non-zero, append a revision marker to every page's title and body, so the same bundle represents pages edited in Confluence (the only way to make a reimport see a real source change)")
	chain := flag.Bool("chain", false, "emit the pages as a single parent-to-child chain instead of a root with flat children")
	withFindings := flag.Bool("with-findings", false, "also emit a comment, an attachment, restricted pages, and a manifest warning so inspection reports issues")
	corrupt := flag.String("corrupt", "", "deliberately break the bundle: one of "+strings.Join(importfixture.CorruptModes, ", "))
	flag.Parse()

	if *out == "" {
		fatalf("-out is required")
	}
	if *corrupt != "" && !validCorruptMode(*corrupt) {
		fatalf("-corrupt must be one of %s", strings.Join(importfixture.CorruptModes, ", "))
	}

	bundle, err := importfixture.Build(importfixture.Options{
		Pages:          *pages,
		SpaceKey:       *spaceKey,
		SpaceName:      *spaceName,
		OrganizationID: *orgID,
		Team:           *team,
		Revision:       *revision,
		Chain:          *chain,
		WithFindings:   *withFindings,
		Corrupt:        *corrupt,
	})
	if err != nil {
		fatalf("%v", err)
	}
	if err := os.WriteFile(*out, bundle.Zip, 0o600); err != nil {
		fatalf("write %s: %v", *out, err)
	}

	fmt.Printf("wrote %s (%d bytes)\n", *out, len(bundle.Zip))
	fmt.Printf("  archive sha256 : %s\n", bundle.ArchiveSha256())
	fmt.Printf("  jsonl sha256   : %s\n", bundle.JSONLSha256)
	fmt.Printf("  space key/name : %s / %s\n", *spaceKey, *spaceName)
	fmt.Printf("  advisory team  : %s\n", *team)
	fmt.Printf("  pages          : %d\n", bundle.Pages)
	fmt.Printf("  comments       : %d\n", bundle.Comments)
	fmt.Printf("  attachments    : %d\n", bundle.Attachments)
	fmt.Printf("  restricted     : %d\n", bundle.Restricted)
	if *corrupt != "" {
		fmt.Printf("  CORRUPTED      : %s (the API is expected to reject this bundle)\n", *corrupt)
	}
}

func validCorruptMode(mode string) bool {
	return slices.Contains(importfixture.CorruptModes, mode)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "genimportbundle: "+format+"\n", args...)
	os.Exit(1)
}
