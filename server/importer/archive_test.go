package importer

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// zipFixture packs a committed fixture directory into a real archive, in the
// order and with the framing the contract requires.
//
// The exporter ships bundles as ZIPs while the fixtures are committed unpacked
// so they stay reviewable. This closes that gap: the importer is exercised
// against an actual archive, through *zip.Reader, not only against a directory.
func zipFixture(t *testing.T, name string) string {
	t.Helper()

	source := fixtureDir(name)
	output := filepath.Join(t.TempDir(), name+".zip")

	file, err := os.Create(output) //nolint:gosec // path is inside the test's own TempDir
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()

	writer := zip.NewWriter(file)

	var attachments []string
	require.NoError(t, filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if entry := filepath.ToSlash(rel); entry != ManifestFilename && entry != JSONLFilename {
			attachments = append(attachments, entry)
		}
		return nil
	}))
	sort.Strings(attachments)

	for _, entry := range append([]string{ManifestFilename, JSONLFilename}, attachments...) {
		body, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(entry))) //nolint:gosec // fixture path
		require.NoError(t, err)

		header := &zip.FileHeader{Name: entry, Method: zip.Deflate, Modified: time.Unix(0, 0).UTC()}
		w, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	return output
}

func openZipFixture(t *testing.T, name string) *zip.ReadCloser {
	t.Helper()

	reader, err := zip.OpenReader(zipFixture(t, name))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })

	return reader
}

// TestValidateBundleFS_AcceptsARealArchive is the consumer half of the E13
// gate: the same validation that accepts an unpacked fixture must accept the
// packed archive an operator actually hands over.
func TestValidateBundleFS_AcceptsARealArchive(t *testing.T) {
	for _, name := range []string{"minimal", "full"} {
		t.Run(name, func(t *testing.T) {
			manifest, lines, err := ValidateBundleFS(openZipFixture(t, name))
			require.NoError(t, err)

			require.Equal(t, ManifestVersion, manifest.Version)
			require.NotEmpty(t, lines)
			require.Equal(t, LineTypeResolveSpacePlaceholders, lines[len(lines)-1].Type)
		})
	}
}

func TestValidateBundleFS_RejectsInvalidArchives(t *testing.T) {
	for _, fixture := range contractFixtures(t) {
		if fixture.wantErr == "" {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			_, _, err := ValidateBundleFS(openZipFixture(t, fixture.name))
			require.ErrorContains(t, err, fixture.wantErr)
		})
	}
}

// TestArchiveEntryOrder pins the contract's ZIP layout, which an fs.FS view
// cannot express: the manifest is first so a reader can learn the bundle's
// shape and limits before streaming anything else.
func TestArchiveEntryOrder(t *testing.T) {
	reader := openZipFixture(t, "full")

	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}

	require.Equal(t, []string{
		ManifestFilename,
		JSONLFilename,
		"data/262145/393217/diagram.png",
		"data/262145/393218/space_logo.png",
	}, names)

	for _, file := range reader.File {
		require.Truef(t, file.Modified.Equal(time.Unix(0, 0).UTC()),
			"%s carries a non-epoch timestamp", file.Name)
	}
}

// The attachment blobs must survive the round trip through the archive intact,
// since their per-attachment checksums are what the importer verifies.
func TestArchiveAttachmentsRoundTrip(t *testing.T) {
	reader := openZipFixture(t, "full")

	_, lines, err := ValidateBundleFS(reader)
	require.NoError(t, err)

	var checked int
	for _, line := range lines {
		if line.Type != LineTypePage {
			continue
		}
		for _, attachment := range line.Page.Attachments {
			body, err := readArchiveEntry(reader, attachment.Path)
			require.NoError(t, err)

			require.Equal(t, attachment.Props[PropSHA256], SHA256Hex(body))
			checked++
		}
	}
	require.Equal(t, 2, checked)
}

func readArchiveEntry(reader *zip.ReadCloser, name string) ([]byte, error) {
	file, err := reader.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	return io.ReadAll(file)
}

// A path that escapes the bundle root must be rejected from the declaration
// alone, before anything is extracted.
func TestArchiveRejectsUnsafeDeclaredPaths(t *testing.T) {
	_, _, err := ValidateBundleFS(openZipFixture(t, "invalid-unsafe-path"))
	require.ErrorContains(t, err, "not in cleaned form")
	require.True(t, strings.Contains(err.Error(), ".."), "the rejected path is named in the error")
}

// exporterBundlePath is a bundle produced by the real mmetl exporter, from a
// synthetic Confluence backup rather than anyone's private export.
//
//	mmetl transform confluence --file <backup>.zip --space ENG \
//	  --organization-id https://example.atlassian.net --team engineering \
//	  --output server/importer/testdata/exporter/bundle.zip
const exporterBundlePath = "testdata/exporter/bundle.zip"

// TestValidateBundleFS_ExporterProducedBundle validates output from the real
// exporter rather than from this repository's own fixture builders.
//
// The golden fixtures prove both sides agree on a contract they were each
// written against, which cannot catch the shipped exporter drifting away from
// it. This can: if a change to either side breaks the agreement, this test
// fails and the bundle has to be regenerated with the command above.
func TestValidateBundleFS_ExporterProducedBundle(t *testing.T) {
	reader, err := zip.OpenReader(exporterBundlePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()

	manifest, lines, err := ValidateBundleFS(reader)
	require.NoError(t, err)

	require.Equal(t, ManifestVersion, manifest.Version)
	require.Equal(t, Generator, manifest.Generator)
	require.Empty(t, manifest.Errors)
	require.Equal(t, ManifestFilename, reader.File[0].Name, "the manifest must be the first entry")
	require.Equal(t, JSONLFilename, reader.File[1].Name)

	var pages, comments, attachments int
	for _, line := range lines {
		switch line.Type {
		case LineTypePage:
			pages++
			attachments += len(line.Page.Attachments)
		case LineTypePageComment:
			comments++
		}
	}
	require.Equal(t, manifest.Counts.PagesEmitted+manifest.Counts.BlogPostsEmitted, pages)
	require.Equal(t, manifest.Counts.CommentsEmitted, comments)
	require.Equal(t, manifest.Counts.AttachmentsEmitted, attachments)
	require.Len(t, manifest.Users, manifest.Counts.UsersEmitted)
}

// TestExporterBundleCarriesTheShapesTheImporterMustHandle checks that the
// committed bundle still exercises the features it was built to cover. A
// regenerated bundle that quietly lost its hierarchy or its placeholders would
// keep passing every other assertion here while testing far less.
func TestExporterBundleCarriesTheShapesTheImporterMustHandle(t *testing.T) {
	reader, err := zip.OpenReader(exporterBundlePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()

	_, lines, err := ValidateBundleFS(reader)
	require.NoError(t, err)

	var (
		hasChildPage       bool
		hasBlogPost        bool
		hasLabels          bool
		hasAttachment      bool
		hasThreadedReply   bool
		hasPagePlaceholder bool
		hasUserPlaceholder bool
		hasRestrictions    bool
	)

	for _, line := range lines {
		switch line.Type {
		case LineTypePage:
			page := line.Page
			if page.ParentImportSourceID != "" {
				hasChildPage = true
			}
			if page.Props[PropConfluenceContentType] == ContentTypeBlogPost {
				hasBlogPost = true
			}
			if labels, ok := page.Props[PropImportLabels].([]any); ok && len(labels) > 0 {
				hasLabels = true
			}
			if len(page.Attachments) > 0 {
				hasAttachment = true
			}
			if strings.Contains(page.Content, "{{CONF_PAGE_ID:") {
				hasPagePlaceholder = true
			}
			if strings.Contains(page.Content, "{{CONF_USER_ID:") {
				hasUserPlaceholder = true
			}
			if restrictions, ok := page.Props[PropConfluenceRestrictions].(map[string]any); ok && len(restrictions) > 0 {
				hasRestrictions = true

				// Restricted users are named by canonical account ID, the only
				// identity this importer resolves. A placeholder here would mean
				// the exporter had left a reference for someone else to fix.
				for _, field := range []string{"view_users", "edit_users"} {
					values, ok := restrictions[field].([]any)
					require.Truef(t, ok, "%s must be present as a list", field)
					for _, value := range values {
						text, ok := value.(string)
						require.True(t, ok)
						require.NotEmpty(t, text)
						require.NotContains(t, text, "{{")
					}
				}
			}
		case LineTypePageComment:
			comment := line.PageComment
			if comment.ParentCommentImportSourceID != "" {
				hasThreadedReply = true
				require.Equal(t, comment.ParentCommentImportSourceID, comment.ThreadRootImportSourceID,
					"a direct reply's thread root is its parent")
			}
		}
	}

	require.True(t, hasChildPage, "a page hierarchy")
	require.True(t, hasBlogPost, "a blog post imported as a page")
	require.True(t, hasLabels, "preserved labels")
	require.True(t, hasAttachment, "an attachment with its checksum")
	require.True(t, hasThreadedReply, "a comment thread")
	require.True(t, hasPagePlaceholder, "a page-link placeholder to resolve")
	require.True(t, hasUserPlaceholder, "a mention placeholder to resolve")
	require.True(t, hasRestrictions, "a page carrying restrictions to store")
}

// Every attachment the exporter declared must be present with the checksum it
// recorded, since that is what the importer verifies before uploading.
func TestExporterBundleAttachmentsVerify(t *testing.T) {
	reader, err := zip.OpenReader(exporterBundlePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()

	_, lines, err := ValidateBundleFS(reader)
	require.NoError(t, err)

	for _, line := range lines {
		if line.Type != LineTypePage {
			continue
		}
		for _, attachment := range line.Page.Attachments {
			body, err := readArchiveEntry(reader, attachment.Path)
			require.NoErrorf(t, err, "attachment %s is missing", attachment.Path)
			require.Equal(t, attachment.Props[PropSHA256], SHA256Hex(body))
			require.Equal(t, float64(len(body)), attachment.Props[PropSize])
		}
	}
}
