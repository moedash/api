package main

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TestCatalogImportsForDropsUnusedDependency: a file imports two files but
// only references a type from one of them - the unused import must be
// dropped from the regenerated Dependency list.
func TestCatalogImportsForDropsUnusedDependency(t *testing.T) {
	aFile := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("temporal/api/example/v1/a.proto"),
		Package:     proto.String("example.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("A")}},
	}
	bFile := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("temporal/api/example/v1/b.proto"),
		Package:     proto.String("example.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("B")}},
	}
	mainFile := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api/example/v1/main.proto"),
		Package: proto.String("example.v1"),
		Dependency: []string{
			"temporal/api/example/v1/a.proto",
			"temporal/api/example/v1/b.proto",
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Request"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("a_ref"),
				Number:   proto.Int32(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".example.v1.A"),
			}},
		}},
	}

	cat := newCatalog(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{aFile, bFile, mainFile}})
	got := cat.importsFor(mainFile)

	want := []string{"temporal/api/example/v1/a.proto"}
	if !reflect.DeepEqual(got.direct, want) {
		t.Fatalf("importsFor().direct = %v, want %v (b.proto is unused and must be dropped)", got.direct, want)
	}
}

// TestCatalogImportsForRetainsExtensionOnlyDependency: the request_header
// shape found during review - a dependency used only via a custom-option
// extension set on a method (detected by collectOptionImports walking
// unknown wire bytes, since this test process has no Go registration for
// the extension), never via a TypeName reference. Must be retained, not
// dropped as "unused".
func TestCatalogImportsForRetainsExtensionOnlyDependency(t *testing.T) {
	const extNumber = 90010

	optsFile := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api/example/v1/opts.proto"),
		Package: proto.String("example.v1"),
		Extension: []*descriptorpb.FieldDescriptorProto{{
			Name:     proto.String("my_option"),
			Number:   proto.Int32(extNumber),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			Extendee: proto.String(".google.protobuf.MethodOptions"),
		}},
	}

	methodOpts := &descriptorpb.MethodOptions{}
	methodOpts.ProtoReflect().SetUnknown(
		protowire.AppendString(protowire.AppendTag(nil, protowire.Number(extNumber), protowire.BytesType), "some-value"),
	)
	svcFile := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("temporal/api/example/v1/service.proto"),
		Package:    proto.String("example.v1"),
		Dependency: []string{"temporal/api/example/v1/opts.proto"},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Example"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("DoThing"),
				InputType:  proto.String(".example.v1.Req"),
				OutputType: proto.String(".example.v1.Resp"),
				Options:    methodOpts,
			}},
		}},
	}

	cat := newCatalog(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{optsFile, svcFile}})
	got := cat.importsFor(svcFile)

	want := []string{"temporal/api/example/v1/opts.proto"}
	if !reflect.DeepEqual(got.direct, want) {
		t.Fatalf("importsFor().direct = %v, want %v (opts.proto is used only via the my_option extension, must be retained)", got.direct, want)
	}
}

// TestCatalogImportsForRemapsPublicWeakIndexAfterDrop: a.proto (unused,
// dropped), b.proto (unused but marked public - retained anyway), c.proto
// (used - retained). b's PublicDependency index must shift from its old
// position (1) to its new one (0) once a.proto is dropped ahead of it,
// not silently point at whatever unrelated import now sits at index 1.
func TestCatalogImportsForRemapsPublicWeakIndexAfterDrop(t *testing.T) {
	aFile := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("temporal/api/example/v1/a.proto"),
		Package:     proto.String("example.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("A")}},
	}
	bFile := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("temporal/api/example/v1/b.proto"),
		Package:     proto.String("example.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("B")}},
	}
	cFile := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("temporal/api/example/v1/c.proto"),
		Package:     proto.String("example.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("C")}},
	}
	mainFile := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api/example/v1/main.proto"),
		Package: proto.String("example.v1"),
		Dependency: []string{
			"temporal/api/example/v1/a.proto", // unused, not public - dropped
			"temporal/api/example/v1/b.proto", // unused, public - retained anyway
			"temporal/api/example/v1/c.proto", // used - retained
		},
		PublicDependency: []int32{1}, // b.proto
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Request"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("c_ref"),
				Number:   proto.Int32(1),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".example.v1.C"),
			}},
		}},
	}

	cat := newCatalog(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{aFile, bFile, cFile, mainFile}})
	got := cat.importsFor(mainFile)

	wantDirect := []string{"temporal/api/example/v1/b.proto", "temporal/api/example/v1/c.proto"}
	if !reflect.DeepEqual(got.direct, wantDirect) {
		t.Fatalf("importsFor().direct = %v, want %v", got.direct, wantDirect)
	}
	wantPublic := []int32{0} // b.proto, now at index 0 after a.proto was dropped
	if !reflect.DeepEqual(got.public, wantPublic) {
		t.Fatalf("importsFor().public = %v, want %v (b.proto's index must shift from 1 to 0, not point at c.proto)", got.public, wantPublic)
	}
}
