package main

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestStableProjectionOmitsExperimentalFields(t *testing.T) {
	experimentalOptions := &descriptorpb.FieldOptions{}
	experimentalOptions.ProtoReflect().SetUnknown(
		protowire.AppendString(
			protowire.AppendTag(nil, 95134, protowire.BytesType),
			"nexus-operation-tags",
		),
	)
	input := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name: proto.String("temporal/api_next/example/v1/message.proto"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Request"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("stable"), Number: proto.Int32(1)},
				{Name: proto.String("draft"), Number: proto.Int32(2), Options: experimentalOptions},
			},
		}},
	}}}

	stableAST, err := stripExperimental(input)
	if err != nil {
		t.Fatalf("stripExperimental() error = %v", err)
	}
	output := sanitizeImports(stableAST)

	file := output.File[0]
	if got, want := file.GetName(), "temporal/api/example/v1/message.proto"; got != want {
		t.Fatalf("file name = %q, want %q", got, want)
	}
	if got, want := file.GetOptions().GetGoPackage(), "go.temporal.io/api/example/v1;example"; got != want {
		t.Fatalf("go_package = %q, want %q", got, want)
	}
	fields := file.MessageType[0].Field
	if len(fields) != 1 || fields[0].GetName() != "stable" {
		t.Fatalf("fields = %v, want only stable", fields)
	}
	reservedNames := file.MessageType[0].ReservedName
	if len(reservedNames) != 0 {
		t.Fatalf("reserved names = %v, want [] (reservation injection is currently disabled)", reservedNames)
	}
}

func TestValidateStableFieldReferencingDraftTypeFails(t *testing.T) {
	draftMessageOptions := &descriptorpb.MessageOptions{}
	draftMessageOptions.ProtoReflect().SetUnknown(
		protowire.AppendString(
			protowire.AppendTag(nil, extExperimentalMessage, protowire.BytesType),
			"",
		),
	)
	input := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:    proto.String("DraftMsg"),
				Options: draftMessageOptions,
			},
			{
				Name: proto.String("Request"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("ref"),
						Number:   proto.Int32(1),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".example.v1.DraftMsg"),
					},
				},
			},
		},
	}}}

	stableAST, err := stripExperimental(input)
	if err != nil {
		t.Fatalf("stripExperimental() error = %v, want nil (T11: stripExperimental no longer checks type references, only tag placement)", err)
	}
	output := sanitizeImports(stableAST)

	_, err = linkStrippedSet(output)
	if err == nil {
		t.Fatal("linkStrippedSet() error = nil, want a failure (Request.ref points at stripped DraftMsg)")
	}
	if !strings.Contains(err.Error(), "DraftMsg") {
		t.Fatalf("linkStrippedSet() error = %v, want it to mention the unresolved type DraftMsg", err)
	}
	if !strings.Contains(err.Error(), "marked experimental") {
		t.Fatalf("linkStrippedSet() error = %v, want the wrapped hint about experimental types", err)
	}
}

func TestValidateDraftFieldReferencingDraftTypeGeneratesCleanly(t *testing.T) {
	draftMessageOptions := &descriptorpb.MessageOptions{}
	draftMessageOptions.ProtoReflect().SetUnknown(
		protowire.AppendString(
			protowire.AppendTag(nil, extExperimentalMessage, protowire.BytesType),
			"",
		),
	)
	draftFieldOptions := &descriptorpb.FieldOptions{}
	draftFieldOptions.ProtoReflect().SetUnknown(
		protowire.AppendString(
			protowire.AppendTag(nil, extExperimentalField, protowire.BytesType),
			"",
		),
	)
	input := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:    proto.String("DraftMsg"),
				Options: draftMessageOptions,
			},
			{
				Name: proto.String("Request"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:     proto.String("ref"),
						Number:   proto.Int32(1),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".example.v1.DraftMsg"),
						Options:  draftFieldOptions,
					},
				},
			},
		},
	}}}

	stableAST, err := stripExperimental(input)
	if err != nil {
		t.Fatalf("stripExperimental() error = %v, want nil", err)
	}
	output := sanitizeImports(stableAST)
	file := output.File[0]
	if len(file.MessageType) != 1 || file.MessageType[0].GetName() != "Request" {
		t.Fatalf("message types = %v, want only Request (DraftMsg stripped)", file.MessageType)
	}
	if fields := file.MessageType[0].Field; len(fields) != 0 {
		t.Fatalf("fields = %v, want none (draft field stripped)", fields)
	}
	if _, err := linkStrippedSet(output); err != nil {
		t.Fatalf("linkStrippedSet() error = %v, want nil (no dangling reference once both are stripped)", err)
	}
}

func TestValidateRejectsExperimentalOnFileExtension(t *testing.T) {
	input := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		Extension: []*descriptorpb.FieldDescriptorProto{{
			Name:     proto.String("my_option"),
			Number:   proto.Int32(90001),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			Extendee: proto.String(".google.protobuf.MethodOptions"),
			Options:  experimentalFieldOptions(),
		}},
	}}}

	_, err := stripExperimental(input)
	if err == nil {
		t.Fatal("stripExperimental() error = nil, want error for experimental tag on a file-level extension")
	}
	if !strings.Contains(err.Error(), "my_option") {
		t.Fatalf("stripExperimental() error = %v, want it to name the extension (my_option)", err)
	}
}

func TestValidateRejectsExperimentalOnNestedExtension(t *testing.T) {
	input := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Container"),
			Extension: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("my_nested_option"),
				Number:   proto.Int32(90002),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Extendee: proto.String(".google.protobuf.FieldOptions"),
				Options:  experimentalFieldOptions(),
			}},
		}},
	}}}

	_, err := stripExperimental(input)
	if err == nil {
		t.Fatal("stripExperimental() error = nil, want error for experimental tag on a nested extension")
	}
	if !strings.Contains(err.Error(), "my_nested_option") {
		t.Fatalf("stripExperimental() error = %v, want it to name the extension (my_nested_option)", err)
	}
}
