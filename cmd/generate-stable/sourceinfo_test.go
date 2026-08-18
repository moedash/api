package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jhump/protoreflect/v2/sourceloc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestRemapPathDropsRemovedElement(t *testing.T) {
	// message_type[0] and message_type[2] existed originally; only
	// message_type[0] (kept at 0) and message_type[2] (kept, renumbered to
	// 1) survive stripping - message_type[1] was dropped.
	allOrigPaths := map[string]bool{
		pathKey([]int32{fileMessageTypeField, 0}): true,
		pathKey([]int32{fileMessageTypeField, 1}): true,
		pathKey([]int32{fileMessageTypeField, 2}): true,
	}
	remap := map[string]protoreflect.SourcePath{
		pathKey([]int32{fileMessageTypeField, 0}): {fileMessageTypeField, 0},
		pathKey([]int32{fileMessageTypeField, 2}): {fileMessageTypeField, 1},
	}

	tests := []struct {
		name     string
		path     []int32
		wantPath []int32
		wantOK   bool
	}{
		{"kept first message", []int32{fileMessageTypeField, 0}, []int32{fileMessageTypeField, 0}, true},
		{"dropped middle message", []int32{fileMessageTypeField, 1}, nil, false},
		{"kept third message renumbered", []int32{fileMessageTypeField, 2}, []int32{fileMessageTypeField, 1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotOK := remapPath(tt.path, remap, allOrigPaths)
			if gotOK != tt.wantOK {
				t.Fatalf("remapPath(%v) ok = %v, want %v", tt.path, gotOK, tt.wantOK)
			}
			if gotOK && !reflect.DeepEqual(gotPath, tt.wantPath) {
				t.Fatalf("remapPath(%v) = %v, want %v", tt.path, gotPath, tt.wantPath)
			}
		})
	}
}

func TestRemapPathPassesThroughUntrackedAndTrailingComponents(t *testing.T) {
	// message_type[0]'s field[3] survives, renumbered to field[1].
	allOrigPaths := map[string]bool{
		pathKey([]int32{fileMessageTypeField, 0}):                   true,
		pathKey([]int32{fileMessageTypeField, 0, msgFieldField, 3}): true,
	}
	remap := map[string]protoreflect.SourcePath{
		pathKey([]int32{fileMessageTypeField, 0}):                   {fileMessageTypeField, 0},
		pathKey([]int32{fileMessageTypeField, 0, msgFieldField, 3}): {fileMessageTypeField, 0, msgFieldField, 1},
	}

	tests := []struct {
		name     string
		path     []int32
		wantPath []int32
	}{
		{
			name:     "syntax has no index pair, passes through",
			path:     []int32{12},
			wantPath: []int32{12},
		},
		{
			name:     "trailing scalar sub-field (field name) passes through untouched",
			path:     []int32{fileMessageTypeField, 0, msgFieldField, 3, 1},
			wantPath: []int32{fileMessageTypeField, 0, msgFieldField, 1, 1},
		},
		{
			// FileDescriptorProto.extension = field 7; never walked by
			// walkCommentableDescriptors because extensions are never
			// stripped (see schema.go), so it never appears in
			// allOrigPaths and passes through whole.
			name:     "untracked container (extension) passes through entirely",
			path:     []int32{7, 5, 1},
			wantPath: []int32{7, 5, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, ok := remapPath(tt.path, remap, allOrigPaths)
			if !ok {
				t.Fatalf("remapPath(%v) ok = false, want true", tt.path)
			}
			if !reflect.DeepEqual(gotPath, tt.wantPath) {
				t.Fatalf("remapPath(%v) = %v, want %v", tt.path, gotPath, tt.wantPath)
			}
		})
	}
}

func TestRemapPathNestedContainers(t *testing.T) {
	// file.message_type[1] ("Outer") kept at 0. Outer.nested_type[2]
	// ("Inner") kept at 0; Outer.nested_type[0] dropped. Inner.field[4] kept
	// at 0.
	allOrigPaths := map[string]bool{
		pathKey([]int32{fileMessageTypeField, 1}):                                          true,
		pathKey([]int32{fileMessageTypeField, 1, msgNestedTypeField, 0}):                   true,
		pathKey([]int32{fileMessageTypeField, 1, msgNestedTypeField, 2}):                   true,
		pathKey([]int32{fileMessageTypeField, 1, msgNestedTypeField, 2, msgFieldField, 4}): true,
		pathKey([]int32{fileMessageTypeField, 0}):                                          true, // a whole other dropped top-level message
	}
	remap := map[string]protoreflect.SourcePath{
		pathKey([]int32{fileMessageTypeField, 1}):                                          {fileMessageTypeField, 0},
		pathKey([]int32{fileMessageTypeField, 1, msgNestedTypeField, 2}):                   {fileMessageTypeField, 0, msgNestedTypeField, 0},
		pathKey([]int32{fileMessageTypeField, 1, msgNestedTypeField, 2, msgFieldField, 4}): {fileMessageTypeField, 0, msgNestedTypeField, 0, msgFieldField, 0},
	}

	// Path to Outer.Inner.field[4].
	gotPath, ok := remapPath([]int32{fileMessageTypeField, 1, msgNestedTypeField, 2, msgFieldField, 4}, remap, allOrigPaths)
	if !ok {
		t.Fatal("remapPath: ok = false, want true")
	}
	want := []int32{fileMessageTypeField, 0, msgNestedTypeField, 0, msgFieldField, 0}
	if !reflect.DeepEqual(gotPath, want) {
		t.Fatalf("remapPath = %v, want %v", gotPath, want)
	}

	// Path to a dropped nested type: Outer.nested_type[0].
	_, ok = remapPath([]int32{fileMessageTypeField, 1, msgNestedTypeField, 0}, remap, allOrigPaths)
	if ok {
		t.Fatal("remapPath: ok = true for dropped nested_type, want false")
	}

	// Path to a message that itself was dropped entirely (file.message_type[0]).
	_, ok = remapPath([]int32{fileMessageTypeField, 0}, remap, allOrigPaths)
	if ok {
		t.Fatal("remapPath: ok = true for dropped message_type, want false")
	}
}

// TestRemapPathDoesNotFallBackToSurvivingAncestor is the regression case
// this design exists to prevent: a location addressing a dropped field
// (draft_field) must not fall back to matching its still-surviving
// enclosing message and be reinterpreted against a sibling's new index.
// Caught by the prototype run before this became production code - see
// worklist T2.
func TestRemapPathDoesNotFallBackToSurvivingAncestor(t *testing.T) {
	// Foo (message_type[0]) survives unchanged. Its field[1] (draft_field)
	// is dropped; field[2] (beta) survives, renumbered to field[1].
	allOrigPaths := map[string]bool{
		pathKey([]int32{fileMessageTypeField, 0}):                   true,
		pathKey([]int32{fileMessageTypeField, 0, msgFieldField, 1}): true, // draft_field
		pathKey([]int32{fileMessageTypeField, 0, msgFieldField, 2}): true, // beta
	}
	remap := map[string]protoreflect.SourcePath{
		pathKey([]int32{fileMessageTypeField, 0}):                   {fileMessageTypeField, 0},
		pathKey([]int32{fileMessageTypeField, 0, msgFieldField, 2}): {fileMessageTypeField, 0, msgFieldField, 1},
	}

	_, ok := remapPath([]int32{fileMessageTypeField, 0, msgFieldField, 1}, remap, allOrigPaths)
	if ok {
		t.Fatal("remapPath: ok = true for dropped field's comment, want false (must not fall back to the surviving enclosing message)")
	}
}

// buildCommentFixture is stripLinkPrint's fixture (TestGoldenCommentRetentionOnMiddleFieldStrip)
// minus gamma - just enough to exercise a real strip (draft_field, middle of
// the list) with real comments on the survivors. Shared by the
// validateSourceCodeInfo tests below, which need direct access to the
// intermediate linked descriptors that stripLinkPrint doesn't return.
func buildCommentFixture() *descriptorpb.FileDescriptorProto {
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

	locFor := func(fieldIdx int32, comment string) *descriptorpb.SourceCodeInfo_Location {
		path := append(append([]int32{}, msgPath...), msgFieldField, fieldIdx)
		return &descriptorpb.SourceCodeInfo_Location{
			Path:            path,
			Span:            []int32{fieldIdx + 1, 0, 10},
			LeadingComments: proto.String(" " + comment + "\n"),
		}
	}

	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:  proto.String("Foo"),
			Field: []*descriptorpb.FieldDescriptorProto{alpha, draftField, beta},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{Path: []int32{fileMessageTypeField, 0}, Span: []int32{0, 0, 1}, LeadingComments: proto.String(" Foo message\n")},
				locFor(0, "COMMENT FOR alpha"),
				locFor(1, "COMMENT FOR draft_field"),
				locFor(2, "COMMENT FOR beta"),
			},
		},
	}
}

// TestValidateSourceCodeInfoPassesOnCorrectRebuild is the positive control
// for the two negative tests below: proves validateSourceCodeInfo doesn't
// false-positive on a real, correct rebuildSourceCodeInfo output. Without
// this, a validator that always errored (or always passed) would look
// identical to a working one in the negative tests alone.
func TestValidateSourceCodeInfoPassesOnCorrectRebuild(t *testing.T) {
	file := buildCommentFixture()
	originalFD := linkStandalone(t, file)

	stripped := stripFile(file)
	prelim := linkStandalone(t, stripped)
	rebuilt := rebuildSourceCodeInfo(file, originalFD, prelim)

	if err := validateSourceCodeInfo(file, originalFD, prelim, rebuilt); err != nil {
		t.Fatalf("validateSourceCodeInfo() error = %v, want nil for a correct rebuild", err)
	}
}

// TestValidateSourceCodeInfoCatchesWrongCommentText simulates the exact
// class of regression validateSourceCodeInfo exists to catch (review notes,
// .agents/worklist.md Aug 17): rebuildSourceCodeInfo attaches the wrong
// comment text to a surviving declaration. Corrupts a real, otherwise-correct
// rebuild rather than hand-crafting a bad SourceCodeInfo from scratch, so the
// only thing under test is the validator's own detection logic.
func TestValidateSourceCodeInfoCatchesWrongCommentText(t *testing.T) {
	file := buildCommentFixture()
	originalFD := linkStandalone(t, file)

	stripped := stripFile(file)
	prelim := linkStandalone(t, stripped)
	rebuilt := rebuildSourceCodeInfo(file, originalFD, prelim)

	corrupted := proto.Clone(rebuilt).(*descriptorpb.SourceCodeInfo)
	found := false
	for _, loc := range corrupted.GetLocation() {
		if loc.GetLeadingComments() == " COMMENT FOR beta\n" {
			loc.LeadingComments = proto.String(" WRONG COMMENT\n")
			found = true
		}
	}
	if !found {
		t.Fatal("test setup: didn't find beta's comment location to corrupt")
	}

	err := validateSourceCodeInfo(file, originalFD, prelim, corrupted)
	if err == nil {
		t.Fatal("validateSourceCodeInfo() error = nil, want a failure for a corrupted comment")
	}
	if !strings.Contains(err.Error(), "beta") {
		t.Fatalf("validateSourceCodeInfo() error = %v, want it to name the affected declaration (beta)", err)
	}
}

// TestValidateSourceCodeInfoCatchesLostComment is the other failure shape:
// a surviving declaration's Location is missing from the rebuild entirely
// (comment silently vanished) rather than present with wrong text.
func TestValidateSourceCodeInfoCatchesLostComment(t *testing.T) {
	file := buildCommentFixture()
	originalFD := linkStandalone(t, file)

	stripped := stripFile(file)
	prelim := linkStandalone(t, stripped)
	rebuilt := rebuildSourceCodeInfo(file, originalFD, prelim)

	corrupted := &descriptorpb.SourceCodeInfo{}
	removed := false
	for _, loc := range rebuilt.GetLocation() {
		if loc.GetLeadingComments() == " COMMENT FOR beta\n" {
			removed = true
			continue
		}
		corrupted.Location = append(corrupted.Location, loc)
	}
	if !removed {
		t.Fatal("test setup: didn't find beta's comment location to remove")
	}

	err := validateSourceCodeInfo(file, originalFD, prelim, corrupted)
	if err == nil {
		t.Fatal("validateSourceCodeInfo() error = nil, want a failure for a lost comment")
	}
	if !strings.Contains(err.Error(), "beta") {
		t.Fatalf("validateSourceCodeInfo() error = %v, want it to name the affected declaration (beta)", err)
	}
}

// TestTrailingAndDetachedCommentsSurviveStripping is T14 (worklist Aug 17):
// every existing fixture across this package only ever set LeadingComments.
// rebuildSourceCodeInfo clones the whole Location and only rewrites Path, so
// TrailingComments/LeadingDetachedComments are expected to travel identically
// - this proves it rather than assuming it from the code path, using
// validateSourceCodeInfo itself as the check (it compares all three comment
// fields, not just LeadingComments), plus a direct check that a stripped
// field's trailing comment doesn't leak onto the next surviving field.
func TestTrailingAndDetachedCommentsSurviveStripping(t *testing.T) {
	msgPath := []int32{fileMessageTypeField, 0}
	fieldPath := func(idx int32) []int32 {
		return append(append([]int32{}, msgPath...), msgFieldField, idx)
	}

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

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("temporal/api_next/example/v1/message.proto"),
		Package: proto.String("example.v1"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("go.temporal.io/api/example/v1;example"),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:  proto.String("Foo"),
			Field: []*descriptorpb.FieldDescriptorProto{alpha, draftField, beta},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{
			Location: []*descriptorpb.SourceCodeInfo_Location{
				{Path: msgPath, Span: []int32{0, 0, 1}, LeadingComments: proto.String(" Foo message\n")},
				{
					Path:             fieldPath(0),
					Span:             []int32{1, 0, 10},
					TrailingComments: proto.String(" inline trailing comment for alpha\n"),
				},
				{
					Path:             fieldPath(1),
					Span:             []int32{2, 0, 10},
					TrailingComments: proto.String(" inline trailing comment for draft_field - must not survive\n"),
				},
				{
					Path:                    fieldPath(2),
					Span:                    []int32{3, 0, 10},
					LeadingComments:         proto.String(" leading comment for beta\n"),
					LeadingDetachedComments: []string{" detached paragraph above beta, separated by a blank line\n"},
				},
			},
		},
	}

	originalFD := linkStandalone(t, file)
	stripped := stripFile(file)
	prelim := linkStandalone(t, stripped)
	rebuilt := rebuildSourceCodeInfo(file, originalFD, prelim)

	if err := validateSourceCodeInfo(file, originalFD, prelim, rebuilt); err != nil {
		t.Fatalf("validateSourceCodeInfo() error = %v, want nil - trailing/detached comments should round-trip like leading comments", err)
	}

	betaFD := prelim.Messages().Get(0).Fields().ByName("beta")
	if betaFD == nil {
		t.Fatal("test setup: beta not found in stripped/linked descriptor")
	}
	betaNewPath := pathKey(sourceloc.PathFor(betaFD))
	var betaLoc *descriptorpb.SourceCodeInfo_Location
	for _, loc := range rebuilt.GetLocation() {
		if pathKey(loc.GetPath()) == betaNewPath {
			betaLoc = loc
		}
	}
	if betaLoc == nil {
		t.Fatal("beta's rebuilt Location not found")
	}
	if betaLoc.GetTrailingComments() != "" {
		t.Fatalf("beta's TrailingComments = %q, want empty (draft_field's trailing comment must not leak onto beta)", betaLoc.GetTrailingComments())
	}
	if got, want := betaLoc.GetLeadingComments(), " leading comment for beta\n"; got != want {
		t.Fatalf("beta's LeadingComments = %q, want %q", got, want)
	}
	wantDetached := []string{" detached paragraph above beta, separated by a blank line\n"}
	if got := betaLoc.GetLeadingDetachedComments(); !slices.Equal(got, wantDetached) {
		t.Fatalf("beta's LeadingDetachedComments = %v, want %v", got, wantDetached)
	}
}
