package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func LoadProfile(path string) (Profile, error) {
	var profile Profile
	if err := loadJSON(path, &profile); err != nil {
		return Profile{}, fmt.Errorf("load profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, fmt.Errorf("validate profile: %w", err)
	}
	return profile, nil
}

func LoadManifest(path string) (Manifest, error) {
	var manifest Manifest
	if err := loadJSON(path, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("load manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate manifest: %w", err)
	}
	return manifest, nil
}

func LoadObservationSuite(path string) (ObservationSuite, error) {
	var suite ObservationSuite
	if err := loadJSON(path, &suite); err != nil {
		return ObservationSuite{}, fmt.Errorf("load observation suite: %w", err)
	}
	if err := suite.Validate(); err != nil {
		return ObservationSuite{}, fmt.Errorf("validate observation suite: %w", err)
	}
	return suite, nil
}

func DecodeProfile(reader io.Reader) (Profile, error) {
	var profile Profile
	if err := decodeStrict(reader, &profile); err != nil {
		return Profile{}, err
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func DecodeManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	if err := decodeStrict(reader, &manifest); err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func DecodeObservationSuite(reader io.Reader) (ObservationSuite, error) {
	var suite ObservationSuite
	if err := decodeStrict(reader, &suite); err != nil {
		return ObservationSuite{}, err
	}
	if err := suite.Validate(); err != nil {
		return ObservationSuite{}, err
	}
	return suite, nil
}

func loadJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := decodeStrict(file, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func decodeStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}
