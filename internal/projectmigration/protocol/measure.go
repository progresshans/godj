package protocol

import (
	"errors"

	"github.com/progresshans/godj/internal/projectwire"
	"github.com/progresshans/godj/internal/wirejson"
)

var errResponseTooLarge = errors.New("project makemigrations protocol: response exceeds maximum size")

func measureSuccessDocument(result wireResult) error {
	sizer := wirejson.NewSizer(MaxResponseBytes)
	if !sizer.Literal(`{"protocol_version":1,"status":"ok","result":`) ||
		!measureResult(sizer, result) || !sizer.Literal(`}`) {
		return errResponseTooLarge
	}
	return nil
}

func measureResult(sizer *wirejson.Sizer, result wireResult) bool {
	if !sizer.Literal(`{"writer_root":`) || !sizer.String(result.WriterRoot) ||
		!sizer.Literal(`,"project_spec":`) || !projectwire.Measure(sizer, result.ProjectSpec) ||
		!sizer.Literal(`,"project_spec_digest":`) || !sizer.String(result.ProjectSpecDigest) ||
		!sizer.Literal(`,"project_snapshot_sha256":`) || !sizer.String(result.ProjectSnapshotSHA256) ||
		!sizer.Literal(`,"filesystem_catalog":`) || !measureCatalogSummary(sizer, result.FilesystemCatalog) ||
		!sizer.Literal(`,"programmatic_catalog":`) || !measureProgrammaticCatalog(sizer, result.ProgrammaticCatalog) ||
		!sizer.Literal(`,"definition_set_digest":`) || !sizer.String(result.DefinitionSetDigest) ||
		!sizer.Literal(`,"candidates":[`) {
		return false
	}
	for index := range result.Candidates {
		if index != 0 && !sizer.Literal(`,`) {
			return false
		}
		candidate := result.Candidates[index]
		if !sizer.Literal(`{"app":`) || !sizer.String(candidate.App) ||
			!sizer.Literal(`,"name":`) || !sizer.String(candidate.Name) ||
			!sizer.Literal(`,"document":`) || !sizer.Bytes(candidate.Document) || !sizer.Literal(`}`) {
			return false
		}
	}
	return sizer.Literal(`]}`)
}

func measureCatalogSummary(sizer *wirejson.Sizer, summary wireCatalogSummary) bool {
	return sizer.Literal(`{"source_count":`) && sizer.Integer(int64(summary.SourceCount)) &&
		sizer.Literal(`,"document_bytes":`) && sizer.Integer(int64(summary.DocumentBytes)) &&
		sizer.Literal(`,"digest":`) && sizer.String(summary.Digest) && sizer.Literal(`}`)
}

func measureProgrammaticCatalog(sizer *wirejson.Sizer, catalog wireProgrammaticCatalog) bool {
	if !sizer.Literal(`{"source_count":`) || !sizer.Integer(int64(catalog.SourceCount)) ||
		!sizer.Literal(`,"document_bytes":`) || !sizer.Integer(int64(catalog.DocumentBytes)) ||
		!sizer.Literal(`,"digest":`) || !sizer.String(catalog.Digest) ||
		!sizer.Literal(`,"sources":[`) {
		return false
	}
	for index := range catalog.Sources {
		if index != 0 && !sizer.Literal(`,`) {
			return false
		}
		source := catalog.Sources[index]
		if !sizer.Literal(`{"source_id":`) || !sizer.String(source.SourceID) ||
			!sizer.Literal(`,"document":`) || !sizer.Bytes(source.Document) || !sizer.Literal(`}`) {
			return false
		}
	}
	return sizer.Literal(`]}`)
}

func measureProjectSpec(spec projectwire.Spec, maximum int) (int, error) {
	sizer := wirejson.NewSizer(maximum)
	if !projectwire.Measure(sizer, spec) {
		return 0, errResponseTooLarge
	}
	return sizer.Size(), nil
}
