package main

import (
	"fmt"
	"strings"

	"github.com/jhump/protoreflect/v2/protoprint"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	apiNextPrefix   = "temporal/api_next/"
	stableAPIPrefix = "temporal/api/"

	generatedHeader = "// Generated from api_next. DO NOT EDIT.\n\n"
)

// isAPINextPath/isStableAPIPath name the apiNextPrefix/stableAPIPrefix checks
// scattered across this package (T15, worklist Aug 17) - purely cosmetic, one
// place to change if the prefix convention ever does.
func isAPINextPath(path string) bool {
	return strings.HasPrefix(path, apiNextPrefix)
}

func isStableAPIPath(path string) bool {
	return strings.HasPrefix(path, stableAPIPrefix)
}

var nextOnlyFiles = map[string]bool{
	apiNextPrefix + "protometa/v1/experimental.proto": true,
}

// skip generating files for known experimental protos.
// Intended to be used only by the experimental annotations file itself,
// all others should just use the annotations instead
func isNextOnlyPath(path string) bool {
	return nextOnlyFiles[path]
}

func linkStrippedSet(output *descriptorpb.FileDescriptorSet) (*protoregistry.Files, error) {
	files, err := protodesc.NewFiles(output)
	if err != nil {
		return nil, fmt.Errorf("link stripped descriptor set (check first whether a stable declaration references a type marked experimental): %w", err)
	}
	return files, nil
}

func main() {
	protogen.Options{}.Run(generate)
}

func generate(plugin *protogen.Plugin) error {
	input := &descriptorpb.FileDescriptorSet{File: plugin.Request.ProtoFile}

	// PHASE 1: Type Handling (Strip experimental nodes)
	stableSet, err := stripExperimental(input)
	if err != nil {
		return err
	}

	// PHASE 2: Import & Cyclical Cleanup
	output := sanitizeImports(stableSet)

	// PHASE 3: SourceCodeInfo is still wrong at this point
	// See fixSourceCodeInfo in sourceinfo.go
	if err := fixSourceCodeInfo(plugin, output); err != nil {
		return err
	}
	// PHASE 4: link the final stable descriptor set (now with correct
	// SourceCodeInfo) and print it.
	return writeGeneratedFiles(plugin, output)
}

// writeGeneratedFiles prints each api_next file's stable projection from the
// final linked descriptor set and registers it as generated plugin output.
func writeGeneratedFiles(plugin *protogen.Plugin, output *descriptorpb.FileDescriptorSet) error {
	files, err := linkStrippedSet(output)
	if err != nil {
		return err
	}
	printer := protoprint.Printer{}
	// iterate over the files in plugin.Files to avoid generating protos for dependencies/non-api-next files
	for _, file := range plugin.Files {
		if !file.Generate || !isAPINextPath(file.Desc.Path()) || isNextOnlyPath(file.Desc.Path()) {
			continue
		}
		stableFile, err := files.FindFileByPath(stablePath(file.Desc.Path()))
		if err != nil {
			return fmt.Errorf("find projected file %q: %w", file.Desc.Path(), err)
		}
		content, err := printer.PrintProtoToString(stableFile)
		if err != nil {
			return fmt.Errorf("print projected file %q: %w", stableFile.Path(), err)
		}
		plugin.NewGeneratedFile(stableFile.Path(), "").P(generatedHeader + content)
	}
	return nil
}
