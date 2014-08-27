package validation

import (
	"net/url"

	errs "github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/openshift/origin/pkg/build/api"
)

// ValidateBuild tests required fields for a Build.
func ValidateBuild(build *api.Build) errs.ErrorList {
	allErrs := errs.ErrorList{}
	if len(build.ID) == 0 {
		allErrs = append(allErrs, errs.NewFieldRequired("id", build.ID))
	}
	allErrs = append(allErrs, validateConfigFields(&build.Config).Prefix("Config")...)
	return allErrs
}

// ValidateBuildConfig tests required fields for a Build.
func ValidateBuildConfig(config *api.BuildConfig) errs.ErrorList {
	allErrs := errs.ErrorList{}
	if len(config.ID) == 0 {
		allErrs = append(allErrs, errs.NewFieldRequired("id", config.ID))
	}
	allErrs = append(allErrs, validateConfigFields(config)...)
	return allErrs
}

func validateConfigFields(config *api.BuildConfig) errs.ErrorList {
	allErrs := errs.ErrorList{}
	if len(config.SourceURI) == 0 {
		allErrs = append(allErrs, errs.NewFieldRequired("SourceURI", config.SourceURI))
	} else if !isValidURL(config.SourceURI) {
		// Note: for now require that SourceURI be a valid URL
		allErrs = append(allErrs, errs.NewFieldInvalid("SourceURI", config.SourceURI))
	}
	if len(config.ImageTag) == 0 {
		allErrs = append(allErrs, errs.NewFieldRequired("ImageTag", config.ImageTag))
	}
	if config.Type == api.STIBuildType {
		if len(config.BuilderImage) == 0 {
			allErrs = append(allErrs, errs.NewFieldRequired("BuilderImage", config.BuilderImage))
		}
	} else {
		if len(config.BuilderImage) != 0 {
			allErrs = append(allErrs, errs.NewFieldInvalid("BuilderImage", config.BuilderImage))
		}
	}
	return allErrs
}

func isValidURL(uri string) bool {
	_, err := url.Parse(uri)
	return err == nil
}
