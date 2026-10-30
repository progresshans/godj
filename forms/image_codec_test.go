package forms_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

func TestImageCodecFormAndSetExtensionAndFailureSemantics(t *testing.T) {
	payloads := map[string]string{}
	for _, source := range []struct{ name, prefix string }{{"image-codecs-django61.json", ""}, {"apng-django61.json", "apng_"}, {"webp-animation-django61.json", "webp_"}} {
		var reference struct{ Payloads map[string]string }
		raw, err := os.ReadFile("../uploads/testdata/" + source.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &reference); err != nil {
			t.Fatal(err)
		}
		for name, value := range reference.Payloads {
			payloads[source.prefix+name] = value
		}
	}
	for _, test := range []struct {
		payload, name, format, mime, code string
		frames, calls                     int
		limits                            uploads.ImageLimits
	}{
		{"bmp_palette", "photo.BMP", "bmp", "image/bmp", "", 1, 1, uploads.ImageLimits{}},
		{"dib_rgb", "photo.DIB", "dib", "image/bmp", "", 1, 1, uploads.ImageLimits{}},
		{"tiff_pages", "photo.TIF", "tiff", "image/tiff", "", 2, 1, uploads.ImageLimits{}},
		{"tiff_bigendian16", "photo.tiff", "tiff", "image/tiff", "", 1, 1, uploads.ImageLimits{}},
		{"tiff_raw", "photo.bmp", "tiff", "image/tiff", "", 1, 1, uploads.ImageLimits{}},
		{"dib_rgb", "photo.png", "dib", "image/bmp", "", 1, 1, uploads.ImageLimits{}},
		{"bmp_rgb", "photo.txt", "bmp", "image/bmp", "invalid_extension", 1, 1, uploads.ImageLimits{}},
		{"tiff_pages", "photo.tif", "", "", "image_frames", 0, 0, uploads.ImageLimits{MaxFrames: 1}},
		{"tiff_late_truncated", "photo.txt", "", "", "invalid_image", 0, 0, uploads.ImageLimits{}},
		{"dib_truncated", "photo.dib", "", "", "invalid_image", 0, 0, uploads.ImageLimits{}},
		{"apng_poster", "photo.APNG", "png", "image/png", "", 3, 1, uploads.ImageLimits{}},
		{"apng_subrect", "photo.PNG", "png", "image/png", "", 2, 1, uploads.ImageLimits{}},
		{"apng_palette4", "photo.apng", "png", "image/png", "", 2, 1, uploads.ImageLimits{}},
		{"apng_adam7", "photo.apng", "png", "image/png", "", 2, 1, uploads.ImageLimits{}},
		{"apng_single", "photo.png", "png", "image/png", "", 1, 1, uploads.ImageLimits{}},
		{"apng_rgba", "photo.txt", "png", "image/png", "invalid_extension", 2, 1, uploads.ImageLimits{}},
		{"apng_late_pixel_error", "photo.txt", "", "", "invalid_image", 0, 0, uploads.ImageLimits{}},
		{"apng_poster", "photo.apng", "", "", "image_frames", 0, 0, uploads.ImageLimits{MaxFrames: 2}},
		{"apng_subrect", "photo.apng", "", "", "image_pixels", 0, 0, uploads.ImageLimits{MaxTotalPixels: 7}},
		{"apng_rgba_static", "photo.apng", "png", "image/png", "", 1, 1, uploads.ImageLimits{}},
		{"tiff_raw", "photo.apng", "tiff", "image/tiff", "", 1, 1, uploads.ImageLimits{}},
		{"webp_lossy", "photo.WEBP", "webp", "image/webp", "", 2, 1, uploads.ImageLimits{}},
		{"webp_lossless_alpha", "photo.webp", "webp", "image/webp", "", 2, 1, uploads.ImageLimits{}},
		{"webp_compressed_alpha", "photo.webp", "webp", "image/webp", "", 2, 1, uploads.ImageLimits{}},
		{"webp_mixed", "photo.png", "webp", "image/webp", "", 2, 1, uploads.ImageLimits{}},
		{"webp_single", "photo.webp", "webp", "image/webp", "", 1, 1, uploads.ImageLimits{}},
		{"webp_lossy", "photo.txt", "webp", "image/webp", "invalid_extension", 2, 1, uploads.ImageLimits{}},
		{"webp_late_lossless_pixels", "photo.txt", "", "", "invalid_image", 0, 0, uploads.ImageLimits{}},
		{"webp_truncated_compressed_alpha", "photo.webp", "", "", "invalid_image", 0, 0, uploads.ImageLimits{}},
		{"webp_lossy", "photo.webp", "", "", "image_frames", 0, 0, uploads.ImageLimits{MaxFrames: 1}},
		{"webp_subrect", "photo.webp", "", "", "image_pixels", 0, 0, uploads.ImageLimits{MaxTotalPixels: 7}},
	} {
		t.Run(test.payload+"/"+test.name+"/"+test.code, func(t *testing.T) {
			content, err := base64.StdEncoding.DecodeString(payloads[test.payload])
			if err != nil || len(content) == 0 {
				t.Fatal(err)
			}
			file, err := uploads.NewFile(test.name, "application/x-untrusted", content)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			field, err := forms.ImageField("photo", forms.WithImageLimits(test.limits), forms.WithValidators(forms.FieldValidatorFunc(func(value forms.Value) validation.Errors {
				calls++
				image, ok := value.AsFile()
				info, verified := image.Image()
				if !ok || !verified || info.FormatName() != test.format || info.ContentType() != test.mime || info.Frames() != test.frames {
					t.Fatal("validator lost content metadata")
				}
				return validation.Errors{}
			})))
			if err != nil {
				t.Fatal(err)
			}
			row, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			config := forms.DefaultSetConfig()
			config.Prefix = "photos"
			spec, err := forms.NewSetSpec(row, config)
			if err != nil {
				t.Fatal(err)
			}
			set, err := spec.Bind(t.Context(), forms.NewDataWithFiles(map[string][]string{"photos-TOTAL_FORMS": {"1"}, "photos-INITIAL_FORMS": {"0"}}, map[string][]uploads.File{"photos-0-photo": {file}}), nil)
			if err != nil || set.Valid() != (test.code == "") || calls != test.calls {
				t.Fatal("codec Formset result", err, calls)
			}
			rows := set.Forms()
			if len(rows) != 1 {
				t.Fatal("image row disappeared")
			}
			if test.code != "" {
				if rows[0].Form().Errors().Len() != 1 || string(rows[0].Form().Errors().All()[0].Code()) != test.code {
					t.Fatal("content/extension precedence changed", rows[0].Form().Errors())
				}
			} else {
				value, _ := rows[0].Form().Cleaned().File("photo")
				info, valid := value.Image()
				if !valid || info.Width() != 3 || info.Height() != 2 {
					t.Fatal("successful codec lost dimensions")
				}
			}
			reader, err := file.Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(got, content) || file.ContentType() != "application/x-untrusted" {
				t.Fatal("form binding modified upload", readErr, closeErr)
			}
		})
	}
}
