//go:build darwin || linux

package projectcheck

type generationArguments struct {
	check              bool
	explicitDescriptor string
}

func parseGenerationArguments(argv []string) (generationArguments, *GenerationFailure) {
	switch {
	case len(argv) == 1 && argv[0] == "generate":
		return generationArguments{}, nil
	case len(argv) == 2 && argv[0] == "generate" && argv[1] == "--check":
		return generationArguments{check: true}, nil
	case len(argv) == 3 && argv[0] == "generate" && argv[1] == "--project" && argv[2] != "":
		return generationArguments{explicitDescriptor: argv[2]}, nil
	case len(argv) == 4 && argv[0] == "generate" && argv[1] == "--check" && argv[2] == "--project" && argv[3] != "":
		return generationArguments{check: true, explicitDescriptor: argv[3]}, nil
	default:
		failure := GenerationFailure{Category: GenerationCategoryCommand, Code: GenerationCodeInvalidArguments}
		return generationArguments{}, &failure
	}
}
