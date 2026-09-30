package consumer_test

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"example.com/godj-files/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/storage"
	storagemodel "github.com/progresshans/godj/storage/model"
	"github.com/progresshans/godj/uploads"
)

//go:embed image-codec-reference.json
var nativeImageCodecs []byte

//go:embed apng-reference.json
var nativeAPNG []byte

//go:embed webp-reference.json
var nativeWebP []byte

func runImageCodecs(t *testing.T, database fileBackend, backend storage.Backend, label string, spec forms.Spec) {
	t.Helper()
	type observation struct {
		Django, Python, Pillow string
		Payloads               map[string]string
		Cases                  []struct {
			Name, Filename string
			Valid          bool
			Cleaned        struct {
				Format                string
				ContentType           string `json:"content_type"`
				Width, Height, Frames int
			}
		}
	}
	reference := observation{Payloads: map[string]string{}}
	for _, source := range []struct {
		data   []byte
		prefix string
	}{{nativeImageCodecs, ""}, {nativeAPNG, "apng_"}, {nativeWebP, "webp_"}} {
		var part observation
		if err := json.Unmarshal(source.data, &part); err != nil {
			t.Fatal(err)
		}
		if part.Django != "6.1" || part.Python != "3.14.3" || part.Pillow != "12.3.0" {
			t.Fatal("wrong native codec version")
		}
		for name, value := range part.Payloads {
			reference.Payloads[source.prefix+name] = value
		}
		for _, item := range part.Cases {
			item.Name = source.prefix + item.Name
			reference.Cases = append(reference.Cases, item)
		}
	}
	payload := func(name string) []byte {
		t.Helper()
		content, err := base64.StdEncoding.DecodeString(reference.Payloads[name])
		if err != nil || len(content) == 0 {
			t.Fatal("missing native codec bytes", name, err)
		}
		return content
	}
	selected := map[string]bool{"bmp_palette": true, "dib_rgb": true, "tiff_pages": true, "tiff_bigendian16": true, "tiff_tile": true, "apng_rgba": true, "apng_poster": true, "apng_palette4": true, "apng_adam7": true, "webp_lossy": true, "webp_lossless": true, "webp_compressed_alpha": true, "webp_mixed": true}
	for _, observed := range reference.Cases {
		if !selected[observed.Name] {
			continue
		}
		delete(selected, observed.Name)
		t.Run(observed.Name, func(t *testing.T) {
			ctx := t.Context()
			if !observed.Valid {
				t.Fatal("native codec was not accepted")
			}
			content := payload(observed.Name)
			file, err := uploads.NewFile(observed.Filename, "application/x-untrusted", content)
			if err != nil {
				t.Fatal(err)
			}
			title := label + "-codec-" + observed.Name
			bound, err := formmodel.BindInstance(ctx, models.PhotographObjects, spec, forms.NewDataWithFiles(map[string][]string{"title": {title}, "width": {"999"}}, map[string][]uploads.File{"photo": {file}}), nil, formmodel.PostClean{})
			if err != nil || !bound.BoundForm().Form().Valid() {
				t.Fatal("model codec binding rejected", err)
			}
			prepared, err := bound.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := prepared.Model(); err == nil {
				t.Fatal("unpublished codec became a stored model")
			}
			stored, publications, err := prepared.SaveFiles(ctx, formmodel.FileSaver[models.Photograph]{Field: "photo", Backend: backend, Name: func(value models.Photograph, upload uploads.File) (string, error) {
				if value.Width == nil || value.Height == nil || *value.Width != int64(observed.Cleaned.Width) || *value.Height != int64(observed.Cleaned.Height) {
					return "", errors.New("codec dimensions did not reach typed publication")
				}
				return "codecs/" + observed.Name + "/" + upload.Name(), nil
			}})
			if err != nil || len(publications) != 1 || publications[0].Outcome() != storage.Published {
				t.Fatal("codec was not published", err)
			}
			current, err := stored.Model()
			if err != nil {
				t.Fatal(err)
			}
			if err := stored.Save(ctx, database, &current); err != nil {
				t.Fatal(err)
			}
			rows, err := models.PhotographObjects.Using(database).Filter(models.PhotographFields.ID.Exact(current.ID)).All(ctx)
			if err != nil || len(rows) != 1 || rows[0].Photo == nil || *rows[0].Photo != *current.Photo || !equalImageInteger(rows[0].Width, current.Width) || !equalImageInteger(rows[0].Height, current.Height) {
				t.Fatal("codec name/dimensions did not persist", err)
			}
			current.Width, current.Height = new(int64(19)), new(int64(23))
			refreshed, inspection, err := storagemodel.RefreshImageDimensions(ctx, models.PhotographObjects, current, "photo", backend, uploads.ImageLimits{})
			if err != nil || !inspection.Valid() || inspection.Image().FormatName() != observed.Cleaned.Format || inspection.Image().ContentType() != observed.Cleaned.ContentType || inspection.Image().Frames() != observed.Cleaned.Frames || refreshed.Width == nil || refreshed.Height == nil || *refreshed.Width != int64(observed.Cleaned.Width) || *refreshed.Height != int64(observed.Cleaned.Height) || *current.Width != 19 || *current.Height != 23 {
				t.Fatal("stored codec inspection diverged from upload", err)
			}
			reader, err := backend.Open(ctx, *current.Photo)
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(got, content) {
				t.Fatal("codec storage/refresh rewrote source bytes", readErr, closeErr)
			}
			brokenPayload, brokenName := "tiff_late_truncated", "broken.tiff"
			if strings.HasPrefix(observed.Name, "apng_") {
				brokenPayload, brokenName = "apng_late_pixel_error", "broken.apng"
			}
			if strings.HasPrefix(observed.Name, "webp_") {
				brokenPayload, brokenName = "webp_late_lossless_pixels", "broken.webp"
			}
			broken, err := uploads.NewFile(brokenName, "application/x-untrusted", payload(brokenPayload))
			if err != nil {
				t.Fatal(err)
			}
			rejected, err := formmodel.BindInstance(ctx, models.PhotographObjects, spec, forms.NewDataWithFiles(map[string][]string{"title": {title}}, map[string][]uploads.File{"photo": {broken}}), &rows[0], formmodel.PostClean{})
			if err != nil || rejected.BoundForm().Form().Valid() {
				t.Fatal("bad later image frame became a valid model", err)
			}
			if _, err := rejected.Prepare(); err == nil {
				t.Fatal("bad later page reached typed publication")
			}
		})
	}
	if len(selected) != 0 {
		t.Fatal("required codec observation missing")
	}
}
