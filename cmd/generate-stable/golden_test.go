package main

import (
	"strings"
	"testing"

	"github.com/jhump/protoreflect/v2/protoprint"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	fileMessageTypeField = int32(4) // FileDescriptorProto.message_type
	msgFieldField        = int32(2) // DescriptorProto.field
	msgNestedTypeField   = int32(3) // DescriptorProto.nested_type
	msgOneofDeclField    = int32(8) // DescriptorProto.oneof_decl
)

// experimentalOptions builds a Message/Field/Enum/EnumValue/Service/Method
// Options message carrying only the given experimental extension tag, using
// the unknown-field trick (same approach as main_test.go) since we don't
// depend on the generated extension types here.
func experimentalFieldOptions() *descriptorpb.FieldOptions {
	opts := &descriptorpb.FieldOptions{}
	opts.ProtoReflect().SetUnknown(
		protowire.AppendString(protowire.AppendTag(nil, extExperimentalField, protowire.BytesType), ""),
	)
	return opts
}

func experimentalMessageOptions() *descriptorpb.MessageOptions {
	opts := &descriptorpb.MessageOptions{}
	opts.ProtoReflect().SetUnknown(
		protowire.AppendString(protowire.AppendTag(nil, extExperimentalMessage, protowire.BytesType), ""),
	)
	return opts
}

// linkStandalone links a single dependency-free file (true of every fixture
// in this test file) so its declarations can be walked via protoreflect.
func linkStandalone(t *testing.T, file *descriptorpb.FileDescriptorProto) protoreflect.FileDescriptor {
	t.Helper()
	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{file}})
	if err != nil {
		t.Fatalf("protodesc.NewFiles() error = %v", err)
	}
	fd, err := files.FindFileByPath(file.GetName())
	if err != nil {
		t.Fatalf("FindFileByPath() error = %v", err)
	}
	return fd
}

// stripLinkPrint runs the same sequence main.go does (T2): strip
// structurally, link once to get linked descriptors on both sides, rebuild
// SourceCodeInfo by FullName correlation, then link again (now with correct
// comments) and print. Returns the intermediate stripped proto and its final
// linked descriptor too, since some tests need to assert on those directly
// (field/oneof counts, ContainingOneof) rather than only on printed text.
func stripLinkPrint(t *testing.T, file *descriptorpb.FileDescriptorProto) (stripped *descriptorpb.FileDescriptorProto, fd protoreflect.FileDescriptor, content string) {
	t.Helper()
	originalFD := linkStandalone(t, file)

	stripped = stripFile(file)
	prelim := linkStandalone(t, stripped)
	stripped.SourceCodeInfo = rebuildSourceCodeInfo(file, originalFD, prelim)

	fd = linkStandalone(t, stripped)
	printer := protoprint.Printer{}
	c, err := printer.PrintProtoToString(fd)
	if err != nil {
		t.Fatalf("PrintProtoToString() error = %v", err)
	}
	content = c
	return stripped, fd, content
}

func TestGoldenCommentRetentionOnMiddleFieldStrip(t *testing.T) {
	msgPath := []int32{fileMessageTypeField, 0}

	alpha := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("alpha"), Number: proto.Int32(1),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	}
	draftField := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("draft_field"), Number: proto.Int32(2),
		Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		Options: experimentalFieldOptions(),
	}
	beta := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("beta"), Number: proto.Int32(3),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	}
	gamma := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("gamma"), Number: proto.Int32(4),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	}

	locFor := func(fieldIdx int32, comment string) *descriptorpb.SourceCodeInfo_Location {
		path := append(append([]int32{}, msgPath...), msgFieldField, fieldIdx)
		return &descriptorpb.SourceCodeInfo_Location{
			Path: path,
			// Span is required to be well-formed by protodesc, but goes stale
			// after stripping regardless (worklist T2) - the value doesn't
			// matter for this test, only that it's structurally valid.
			Span:            []int32{fieldIdx + 1, 0, 10},
			LeadingComments: proto.String(" " + comment + "\n"),
		}
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:  proto.String("Foo"),
			Field: []*descriptorpb.FieldDescriptorProto{alpha, draftField, beta, gamma},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{Path: []int32{fileMessageTypeField, 0}, Span: []int32{0, 0, 1}, LeadingComments: proto.String(" Foo message\n")},
				locFor(0, "COMMENT FOR alpha"),
				locFor(1, "COMMENT FOR draft_field"),
				locFor(2, "COMMENT FOR beta"),
				locFor(3, "COMMENT FOR gamma"),
			},
		},
	}

	_, _, content := stripLinkPrint(t, file)

	if strings.Contains(content, "draft_field") {
		t.Fatalf("output contains stripped field draft_field:\n%s", content)
	}
	if strings.Contains(content, "COMMENT FOR draft_field") {
		t.Fatalf("output contains dropped comment COMMENT FOR draft_field - it must not survive on any sibling:\n%s", content)
	}

	assertCommentPrecedesDecl(t, content, "COMMENT FOR alpha", "alpha")
	assertCommentPrecedesDecl(t, content, "COMMENT FOR beta", "beta")
	assertCommentPrecedesDecl(t, content, "COMMENT FOR gamma", "gamma")
}

func TestGoldenOneofStripsOnlyTaggedVariant(t *testing.T) {
	msgPath := []int32{fileMessageTypeField, 0}

	alpha := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("alpha"), Number: proto.Int32(1),
		Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		OneofIndex: proto.Int32(0),
	}
	draftVariant := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("draft_variant"), Number: proto.Int32(2),
		Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		OneofIndex: proto.Int32(0),
		Options:    experimentalFieldOptions(),
	}
	beta := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("beta"), Number: proto.Int32(3),
		Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		OneofIndex: proto.Int32(0),
	}

	locFor := func(fieldIdx int32, comment string) *descriptorpb.SourceCodeInfo_Location {
		path := append(append([]int32{}, msgPath...), msgFieldField, fieldIdx)
		return &descriptorpb.SourceCodeInfo_Location{
			Path:            path,
			Span:            []int32{fieldIdx + 1, 0, 10},
			LeadingComments: proto.String(" " + comment + "\n"),
		}
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:      proto.String("Foo"),
			Field:     []*descriptorpb.FieldDescriptorProto{alpha, draftVariant, beta},
			OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{Path: []int32{fileMessageTypeField, 0}, Span: []int32{0, 0, 1}, LeadingComments: proto.String(" Foo message\n")},
				locFor(0, "COMMENT FOR alpha"),
				locFor(1, "COMMENT FOR draft_variant"),
				locFor(2, "COMMENT FOR beta"),
			},
		},
	}

	stripped, fd, content := stripLinkPrint(t, file)

	if got, want := len(stripped.MessageType[0].OneofDecl), 1; got != want {
		t.Fatalf("OneofDecl count = %d, want %d (oneof survives - alpha/beta still reference it)", got, want)
	}

	// The real correctness signal for OneofIndex re-pointing: a wrong index
	// would either fail to link or put alpha/beta in different oneofs.
	msg := fd.Messages().Get(0)
	alphaFD := msg.Fields().ByName("alpha")
	betaFD := msg.Fields().ByName("beta")
	if alphaFD.ContainingOneof() == nil || betaFD.ContainingOneof() == nil {
		t.Fatalf("alpha/beta lost their oneof")
	}
	if alphaFD.ContainingOneof() != betaFD.ContainingOneof() {
		t.Fatalf("alpha and beta ended up in different oneofs after stripping")
	}

	if strings.Contains(content, "draft_variant") {
		t.Fatalf("output contains stripped variant draft_variant:\n%s", content)
	}
	if !strings.Contains(content, "oneof choice") {
		t.Fatalf("output lost the oneof block entirely, want it to survive with alpha/beta:\n%s", content)
	}
	assertCommentPrecedesDecl(t, content, "COMMENT FOR alpha", "alpha")
	assertCommentPrecedesDecl(t, content, "COMMENT FOR beta", "beta")
}

func TestGoldenOneofDroppedWhenAllVariantsStripped(t *testing.T) {
	draftA := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("draft_a"), Number: proto.Int32(1),
		Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		OneofIndex: proto.Int32(0),
		Options:    experimentalFieldOptions(),
	}
	draftB := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("draft_b"), Number: proto.Int32(2),
		Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		OneofIndex: proto.Int32(0),
		Options:    experimentalFieldOptions(),
	}
	stable := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("stable"), Number: proto.Int32(3),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:      proto.String("Foo"),
			Field:     []*descriptorpb.FieldDescriptorProto{draftA, draftB, stable},
			OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}},
		}},
	}

	stripped, _, content := stripLinkPrint(t, file)

	if got := stripped.MessageType[0].OneofDecl; len(got) != 0 {
		t.Fatalf("OneofDecl = %v, want empty (all variants stripped)", got)
	}
	if got, want := len(stripped.MessageType[0].Field), 1; got != want {
		t.Fatalf("fields = %d, want %d (only 'stable')", got, want)
	}
	if strings.Contains(content, "oneof") {
		t.Fatalf("output still contains an (empty) oneof block:\n%s", content)
	}
}

// TestGoldenNestedMessageRenumbering covers the case a hand-rolled index
// remap needs its own recursive tree structure for: a dropped nested message
// shifting the index of a kept sibling nested message, with a field inside
// the survivor keeping its own comment. Under the FullName-based design this
// falls out of the same rebuildSourceCodeInfo pass with no special-casing.
func TestGoldenNestedMessageRenumbering(t *testing.T) {
	droppedNested := &descriptorpb.DescriptorProto{
		Name:    proto.String("DraftInner"),
		Options: experimentalMessageOptions(),
	}
	keptNested := &descriptorpb.DescriptorProto{
		Name: proto.String("Inner"),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name: proto.String("value"), Number: proto.Int32(1),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}},
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:       proto.String("Outer"),
			NestedType: []*descriptorpb.DescriptorProto{droppedNested, keptNested},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{Path: []int32{fileMessageTypeField, 0}, Span: []int32{0, 0, 1}, LeadingComments: proto.String(" Outer message\n")},
				{Path: []int32{fileMessageTypeField, 0, msgNestedTypeField, 0}, Span: []int32{1, 0, 1}, LeadingComments: proto.String(" DraftInner (dropped)\n")},
				{Path: []int32{fileMessageTypeField, 0, msgNestedTypeField, 1}, Span: []int32{2, 0, 1}, LeadingComments: proto.String(" Inner (kept, was index 1)\n")},
				{Path: []int32{fileMessageTypeField, 0, msgNestedTypeField, 1, msgFieldField, 0}, Span: []int32{3, 0, 1}, LeadingComments: proto.String(" COMMENT FOR value\n")},
			},
		},
	}

	stripped, _, content := stripLinkPrint(t, file)

	if got, want := len(stripped.MessageType[0].NestedType), 1; got != want {
		t.Fatalf("NestedType count = %d, want %d", got, want)
	}
	if strings.Contains(content, "DraftInner") {
		t.Fatalf("output contains stripped nested message DraftInner:\n%s", content)
	}
	if strings.Contains(content, "dropped") {
		t.Fatalf("output contains a comment that belonged to the dropped nested message:\n%s", content)
	}
	assertCommentPrecedesDecl(t, content, "Inner (kept, was index 1)", "message Inner")
	assertCommentPrecedesDecl(t, content, "COMMENT FOR value", "value")
}

// TestGoldenDraftMapFieldDropsSyntheticEntry is T12 (worklist Aug 17): a
// draft-tagged map field's synthetic FooEntry message can't carry its own
// tag (protoc generates it, nobody authors it), so before this fix it always
// survived regardless of the field's tag - an orphaned, unused type left
// sitting in stable. V (the map's value type) is deliberately left stable
// here: this is the sub-case that produced no link error at all before the
// fix, just a silent leftover - the other sub-case (V also draft) is covered
// by TestGoldenDraftMapFieldAndValueTypeBothDropCleanly below.
func TestGoldenDraftMapFieldDropsSyntheticEntry(t *testing.T) {
	v := &descriptorpb.DescriptorProto{
		Name: proto.String("V"),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name: proto.String("value"), Number: proto.Int32(1),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}},
	}
	fooEntry := &descriptorpb.DescriptorProto{
		Name:    proto.String("FooEntry"),
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name: proto.String("key"), Number: proto.Int32(1),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			},
			{
				Name: proto.String("value"), Number: proto.Int32(2),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".example.v1.V"),
			},
		},
	}
	fooField := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("foo"),
		Number:   proto.Int32(1),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(".example.v1.Bar.FooEntry"),
		Options:  experimentalFieldOptions(),
	}
	bar := &descriptorpb.DescriptorProto{
		Name:       proto.String("Bar"),
		Field:      []*descriptorpb.FieldDescriptorProto{fooField},
		NestedType: []*descriptorpb.DescriptorProto{fooEntry},
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{bar, v},
	}

	stripped, _, content := stripLinkPrint(t, file)

	if got, want := len(stripped.MessageType[0].Field), 0; got != want {
		t.Fatalf("Bar fields = %d, want %d (foo stripped)", got, want)
	}
	if got, want := len(stripped.MessageType[0].NestedType), 0; got != want {
		t.Fatalf("Bar nested types = %d, want %d (FooEntry must not survive as an orphan)", got, want)
	}
	if strings.Contains(content, "FooEntry") {
		t.Fatalf("output contains orphaned FooEntry:\n%s", content)
	}
	if strings.Contains(content, "map<") || strings.Contains(content, "map_entry") {
		t.Fatalf("output still contains map-entry trace:\n%s", content)
	}
	if !strings.Contains(content, "message V") {
		t.Fatalf("output lost V - it's stable and unrelated, should survive:\n%s", content)
	}
}

// TestGoldenDraftMapFieldAndValueTypeBothDropCleanly is T12's other sub-case:
// the map field AND its value type are both draft. Before this fix, this
// case still "worked" only by accident - FooEntry.value pointed at a
// stripped type and failed at link time, a real but confusing backstop. With
// the fix, FooEntry is dropped directly alongside foo, so V never needs to
// be reached at all - no link failure required, nothing dangling.
func TestGoldenDraftMapFieldAndValueTypeBothDropCleanly(t *testing.T) {
	v := &descriptorpb.DescriptorProto{
		Name:    proto.String("V"),
		Options: experimentalMessageOptions(),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name: proto.String("value"), Number: proto.Int32(1),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}},
	}
	fooEntry := &descriptorpb.DescriptorProto{
		Name:    proto.String("FooEntry"),
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name: proto.String("key"), Number: proto.Int32(1),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			},
			{
				Name: proto.String("value"), Number: proto.Int32(2),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".example.v1.V"),
			},
		},
	}
	fooField := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("foo"),
		Number:   proto.Int32(1),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(".example.v1.Bar.FooEntry"),
		Options:  experimentalFieldOptions(),
	}
	bar := &descriptorpb.DescriptorProto{
		Name:       proto.String("Bar"),
		Field:      []*descriptorpb.FieldDescriptorProto{fooField},
		NestedType: []*descriptorpb.DescriptorProto{fooEntry},
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{bar, v},
	}

	// stripLinkPrint itself would fatal on a link error - reaching content
	// at all proves no dangling reference was ever created.
	stripped, _, content := stripLinkPrint(t, file)

	if got, want := len(stripped.MessageType[0].NestedType), 0; got != want {
		t.Fatalf("Bar nested types = %d, want %d (FooEntry must drop alongside foo)", got, want)
	}
	if strings.Contains(content, "FooEntry") || strings.Contains(content, "message V") {
		t.Fatalf("output still contains dropped FooEntry or V:\n%s", content)
	}
}

// TestGoldenMapEntrySurvivalIgnoresUnrelatedSameNamedField is the regression
// case for T12's follow-up fix (worklist Aug 17): mapEntryFieldSurvives must
// match a map field by its owning nested type's FULL qualified name, not a
// bare-name suffix. decoyRef is a completely unrelated, stable field whose
// TypeName happens to end in ".FooEntry" too (it points at Elsewhere.FooEntry,
// a plain nested message with the same bare name, nothing to do with the
// map), declared BEFORE the real map field foo. Under the old
// suffix-matching implementation this decoy would have been matched first,
// and being stable, would have made Bar.FooEntry (the actual map entry)
// survive incorrectly even though foo is draft-tagged.
func TestGoldenMapEntrySurvivalIgnoresUnrelatedSameNamedField(t *testing.T) {
	decoyEntry := &descriptorpb.DescriptorProto{
		Name: proto.String("FooEntry"), // same bare name, unrelated, not a map entry
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name: proto.String("value"), Number: proto.Int32(1),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}},
	}
	elsewhere := &descriptorpb.DescriptorProto{
		Name:       proto.String("Elsewhere"),
		NestedType: []*descriptorpb.DescriptorProto{decoyEntry},
	}

	realFooEntry := &descriptorpb.DescriptorProto{
		Name:    proto.String("FooEntry"),
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name: proto.String("key"), Number: proto.Int32(1),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			},
			{
				Name: proto.String("value"), Number: proto.Int32(2),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".example.v1.Elsewhere.FooEntry"), // reuses Elsewhere's string type for value, irrelevant to the point
			},
		},
	}

	decoyRef := &descriptorpb.FieldDescriptorProto{
		Name: proto.String("decoy_ref"), Number: proto.Int32(1),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(".example.v1.Elsewhere.FooEntry"), // unrelated, stable, bare name coincidentally "FooEntry"
	}
	fooField := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("foo"),
		Number:   proto.Int32(2),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(".example.v1.Bar.FooEntry"), // the real map field, draft-tagged
		Options:  experimentalFieldOptions(),
	}
	bar := &descriptorpb.DescriptorProto{
		Name:       proto.String("Bar"),
		Field:      []*descriptorpb.FieldDescriptorProto{decoyRef, fooField}, // decoy declared first
		NestedType: []*descriptorpb.DescriptorProto{realFooEntry},
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{bar, elsewhere},
	}

	stripped, _, content := stripLinkPrint(t, file)

	barMsg := stripped.MessageType[0]
	if got, want := len(barMsg.Field), 1; got != want || barMsg.Field[0].GetName() != "decoy_ref" {
		t.Fatalf("Bar.Field = %v, want only decoy_ref (foo stripped)", barMsg.Field)
	}
	if got, want := len(barMsg.NestedType), 0; got != want {
		t.Fatalf("Bar.NestedType count = %d, want %d - the real map entry must drop despite the decoy's same bare name", got, want)
	}
	if !strings.Contains(content, "message Elsewhere") || !strings.Contains(content, "message FooEntry") {
		t.Fatalf("Elsewhere.FooEntry (the unrelated decoy) must survive untouched:\n%s", content)
	}
}

// assertCommentPrecedesDecl checks that commentText appears as a comment on
// the line(s) immediately preceding (allowing blank lines) the first line
// containing declText, and that no other field's comment intervenes.
func assertCommentPrecedesDecl(t *testing.T, content, commentText, declText string) {
	t.Helper()
	lines := strings.Split(content, "\n")
	declLine := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue // skip comment lines - declText (a field name) may be a substring of its own comment
		}
		if strings.Contains(line, declText) {
			declLine = i
			break
		}
	}
	if declLine == -1 {
		t.Fatalf("declaration for %q not found in output:\n%s", declText, content)
	}
	// Walk backwards over blank lines to find the nearest non-blank line.
	j := declLine - 1
	for j >= 0 && strings.TrimSpace(lines[j]) == "" {
		j--
	}
	if j < 0 || !strings.Contains(lines[j], commentText) {
		got := ""
		if j >= 0 {
			got = lines[j]
		}
		t.Fatalf("expected comment %q immediately before declaration %q, got line %q\nfull output:\n%s", commentText, declText, got, content)
	}
}
