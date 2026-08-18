package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jhump/protoreflect/v2/sourceloc"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func fixSourceCodeInfo(plugin *protogen.Plugin, output *descriptorpb.FileDescriptorSet) error {
	prelim, err := linkStrippedSet(output)
	if err != nil {
		return fmt.Errorf("preliminary link (for source info remap): %w", err)
	}
	for _, file := range plugin.Files {
		if !file.Generate || !isAPINextPath(file.Desc.Path()) || isNextOnlyPath(file.Desc.Path()) {
			continue
		}
		stableFilePath := stablePath(file.Desc.Path())
		strippedFD, err := prelim.FindFileByPath(stableFilePath)
		if err != nil {
			return fmt.Errorf("find projected file %q: %w", file.Desc.Path(), err)
		}
		outFile, err := findFileByName(output, stableFilePath)
		if err != nil {
			return err
		}
		rebuilt := rebuildSourceCodeInfo(file.Proto, file.Desc, strippedFD)
		if err := validateSourceCodeInfo(file.Proto, file.Desc, strippedFD, rebuilt); err != nil {
			return fmt.Errorf("%q: %w", file.Desc.Path(), err)
		}
		outFile.SourceCodeInfo = rebuilt
	}
	return nil
}

func findFileByName(set *descriptorpb.FileDescriptorSet, name string) (*descriptorpb.FileDescriptorProto, error) {
	for _, f := range set.File {
		if f.GetName() == name {
			return f, nil
		}
	}
	return nil, fmt.Errorf("file %q not found in descriptor set", name)
}

func rebuildSourceCodeInfo(originalProto *descriptorpb.FileDescriptorProto, original, stripped protoreflect.FileDescriptor) *descriptorpb.SourceCodeInfo {
	if originalProto.GetSourceCodeInfo() == nil {
		return nil
	}

	origPathByName := map[protoreflect.FullName]protoreflect.SourcePath{}
	allOrigPaths := map[string]bool{}
	walkCommentableDescriptors(original, func(d protoreflect.Descriptor) {
		p := sourceloc.PathFor(d)
		if len(p) == 0 {
			return
		}
		origPathByName[d.FullName()] = p
		allOrigPaths[pathKey(p)] = true
	})

	newPathByName := map[protoreflect.FullName]protoreflect.SourcePath{}
	walkCommentableDescriptors(stripped, func(d protoreflect.Descriptor) {
		p := sourceloc.PathFor(d)
		if len(p) == 0 {
			return
		}
		newPathByName[d.FullName()] = p
	})

	remap := map[string]protoreflect.SourcePath{}
	for name, origPath := range origPathByName {
		if newPath, ok := newPathByName[name]; ok {
			remap[pathKey(origPath)] = newPath
		}
	}

	out := &descriptorpb.SourceCodeInfo{}
	for _, loc := range originalProto.GetSourceCodeInfo().GetLocation() {
		newPath, ok := remapPath(loc.GetPath(), remap, allOrigPaths)
		if !ok {
			continue // addressed a declaration that didn't survive stripping
		}
		newLoc := proto.Clone(loc).(*descriptorpb.SourceCodeInfo_Location)
		newLoc.Path = newPath
		out.Location = append(out.Location, newLoc)
	}
	return out
}

func remapPath(path []int32, remap map[string]protoreflect.SourcePath, allOrigPaths map[string]bool) ([]int32, bool) {
	for l := len(path) - len(path)%2; l >= 2; l -= 2 {
		key := pathKey(path[:l])
		if !allOrigPaths[key] {
			// This prefix was never a real declaration (still inside
			// file-level options/syntax, or an untracked container like
			// extensions) - keep shrinking toward a real boundary.
			continue
		}
		newPrefix, kept := remap[key]
		if !kept {
			// Found the real declaration this path is about, and it did NOT
			// survive stripping. Drop the whole location now - do not keep
			// shrinking to check whether some shorter, enclosing prefix
			// happens to still exist (it might; that's irrelevant here).
			return nil, false
		}
		// Found the real declaration and it survived: splice its new
		// container path onto whatever trailing sub-field tokens (name,
		// number, options...) came after this boundary in the original path.
		result := make([]int32, 0, len(newPrefix)+len(path)-l)
		result = append(result, newPrefix...)
		result = append(result, path[l:]...)
		return result, true
	}
	// No declaration boundary found anywhere in path - it never addressed
	// anything stripping could move (e.g. file-level syntax/options), so
	// pass it through completely unchanged.
	return append([]int32{}, path...), true
}

func pathKey(p []int32) string {
	return fmt.Sprint(p)
}

// walkCommentableDescriptors visits every declaration in fd that can carry a
// doc comment: messages (incl. nested), fields, oneofs, enums (incl. nested)
// and their values, services and their methods. Extensions are deliberately
// excluded - they're never stripped (see schema.go), so their comments never
// need remapping and their locations pass through remapPath untouched.
func walkCommentableDescriptors(fd protoreflect.FileDescriptor, fn func(protoreflect.Descriptor)) {
	for i := 0; i < fd.Messages().Len(); i++ {
		walkMessage(fn, fd.Messages().Get(i))
	}
	for i := 0; i < fd.Enums().Len(); i++ {
		walkEnum(fn, fd.Enums().Get(i))
	}
	for i := 0; i < fd.Services().Len(); i++ {
		sd := fd.Services().Get(i)
		fn(sd)
		for j := 0; j < sd.Methods().Len(); j++ {
			fn(sd.Methods().Get(j))
		}
	}
}

func walkMessage(fn func(protoreflect.Descriptor), md protoreflect.MessageDescriptor) {
	fn(md)
	for i := 0; i < md.Fields().Len(); i++ {
		fn(md.Fields().Get(i))
	}
	for i := 0; i < md.Oneofs().Len(); i++ {
		fn(md.Oneofs().Get(i))
	}
	for i := 0; i < md.Enums().Len(); i++ {
		walkEnum(fn, md.Enums().Get(i))
	}
	for i := 0; i < md.Messages().Len(); i++ {
		walkMessage(fn, md.Messages().Get(i))
	}
}

func walkEnum(fn func(protoreflect.Descriptor), ed protoreflect.EnumDescriptor) {
	fn(ed)
	for i := 0; i < ed.Values().Len(); i++ {
		fn(ed.Values().Get(i))
	}
}

// findByFullName looks up the single descriptor in fd named name, among
// everything walkCommentableDescriptors visits (messages, fields, oneofs,
// enums, enum values, services, methods). Returns ok=false if nothing in fd
// carries that name - which, for stripped, is exactly the "correctly
// dropped" case validateSourceCodeInfo needs to distinguish from a real bug.
func findByFullName(fd protoreflect.FileDescriptor, name protoreflect.FullName) (protoreflect.Descriptor, bool) {
	var found protoreflect.Descriptor
	walkCommentableDescriptors(fd, func(d protoreflect.Descriptor) {
		if found == nil && d.FullName() == name {
			found = d
		}
	})
	return found, found != nil
}

func validateSourceCodeInfo(originalProto *descriptorpb.FileDescriptorProto, original, stripped protoreflect.FileDescriptor, rebuilt *descriptorpb.SourceCodeInfo) error {
	origByPath := map[string]*descriptorpb.SourceCodeInfo_Location{}
	for _, loc := range originalProto.GetSourceCodeInfo().GetLocation() {
		origByPath[pathKey(loc.GetPath())] = loc
	}
	rebuiltByPath := map[string]*descriptorpb.SourceCodeInfo_Location{}
	for _, loc := range rebuilt.GetLocation() {
		rebuiltByPath[pathKey(loc.GetPath())] = loc
	}

	var problems []string
	walkCommentableDescriptors(original, func(d protoreflect.Descriptor) {
		origPath := sourceloc.PathFor(d)
		if len(origPath) == 0 {
			return
		}
		origLoc, hadComment := origByPath[pathKey(origPath)]
		if !hadComment {
			return // nothing to check
		}
		survivor, survived := findByFullName(stripped, d.FullName())
		if !survived {
			return // correctly dropped - the declaration itself is gone, comment loss is expected
		}
		newPath := sourceloc.PathFor(survivor)
		if len(newPath) == 0 {
			problems = append(problems, fmt.Sprintf("%s: survived stripping but has no computable path", d.FullName()))
			return
		}
		rebuiltLoc, ok := rebuiltByPath[pathKey(newPath)]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: comment lost (had %q)", d.FullName(), origLoc.GetLeadingComments()))
			return
		}
		if rebuiltLoc.GetLeadingComments() != origLoc.GetLeadingComments() ||
			rebuiltLoc.GetTrailingComments() != origLoc.GetTrailingComments() ||
			!slices.Equal(rebuiltLoc.GetLeadingDetachedComments(), origLoc.GetLeadingDetachedComments()) {
			problems = append(problems, fmt.Sprintf("%s: comment text changed across strip", d.FullName()))
		}
	})
	if len(problems) > 0 {
		return fmt.Errorf("SourceCodeInfo round-trip failed:\n%s", strings.Join(problems, "\n"))
	}
	return nil
}
