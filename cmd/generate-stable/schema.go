package main

import (
	"fmt"
	"reflect"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	extExperimentalField     = protowire.Number(95134)
	extExperimentalMessage   = protowire.Number(95135)
	extExperimentalEnum      = protowire.Number(95136)
	extExperimentalEnumValue = protowire.Number(95137)
	extExperimentalService   = protowire.Number(95138)
	extExperimentalMethod    = protowire.Number(95139)
)

func filterMessage(msg *descriptorpb.DescriptorProto) bool {
	return isExperimental(msg.GetOptions(), extExperimentalMessage)
}

func filterField(field *descriptorpb.FieldDescriptorProto) bool {
	return isExperimental(field.GetOptions(), extExperimentalField)
}

func filterEnumType(enum *descriptorpb.EnumDescriptorProto) bool {
	return isExperimental(enum.GetOptions(), extExperimentalEnum)
}

func filterEnumValue(value *descriptorpb.EnumValueDescriptorProto) bool {
	return isExperimental(value.GetOptions(), extExperimentalEnumValue)
}

func filterService(service *descriptorpb.ServiceDescriptorProto) bool {
	return isExperimental(service.GetOptions(), extExperimentalService)
}

func filterMethod(method *descriptorpb.MethodDescriptorProto) bool {
	return isExperimental(method.GetOptions(), extExperimentalMethod)
}

func filterExtension(extension *descriptorpb.FieldDescriptorProto) bool {
	// Extensions are essentially fields, so they use the field option.
	return isExperimental(extension.GetOptions(), extExperimentalField)
}

func validateTagsOnNodes(file *descriptorpb.FileDescriptorProto) error {
	for _, msg := range file.MessageType {
		if err := validateMessageTags(msg, file.GetName(), ""); err != nil {
			return err
		}
	}
	for _, ext := range file.Extension {
		if filterExtension(ext) {
			return fmt.Errorf("experimental tags on extensions are not currently supported. Found on extension '%s' in file '%s'", ext.GetName(), file.GetName())
		}
	}
	return nil
}

func validateMessageTags(msg *descriptorpb.DescriptorProto, filename, prefix string) error {
	msgName := joinProtoName(prefix, msg.GetName())
	for _, ext := range msg.Extension {
		if filterExtension(ext) {
			return fmt.Errorf("experimental tags on nested extensions are not currently supported. Found on extension '%s' in message '%s' in file '%s'", ext.GetName(), msg.GetName(), filename)
		}
	}
	for _, nested := range msg.NestedType {
		if err := validateMessageTags(nested, filename, msgName); err != nil {
			return err
		}
	}
	return nil
}

// -------------------------------------------------------------------------
// AST STRIPPING LOGIC
// -------------------------------------------------------------------------

func stripExperimental(inputFiles *descriptorpb.FileDescriptorSet) (*descriptorpb.FileDescriptorSet, error) {
	outputFiles := &descriptorpb.FileDescriptorSet{
		File: make([]*descriptorpb.FileDescriptorProto, 0, len(inputFiles.File)),
	}

	// Validate before stripping (1.5) - tag-placement only; see
	// validateTagsOnNodes for why type-reference checking was removed here
	// (T11, worklist Aug 17).
	for _, file := range inputFiles.File {
		if isAPINextPath(file.GetName()) {
			if err := validateTagsOnNodes(file); err != nil {
				return nil, err
			}
		}
	}

	// Create a stable projection for every api_next file, except the
	// api_next-only ones (see nextOnlyFiles): those are dropped from the
	// output set entirely, so nothing in the stable API can reference them.
	for _, file := range inputFiles.File {
		if isNextOnlyPath(file.GetName()) {
			continue
		}
		if isAPINextPath(file.GetName()) {
			// strip the experimental nodes from the file
			outputFiles.File = append(outputFiles.File, stripFile(file))
		} else {
			// keep the non-api_next files as is
			outputFiles.File = append(outputFiles.File, proto.Clone(file).(*descriptorpb.FileDescriptorProto))
		}
	}

	return outputFiles, nil
}

func stripFile(input *descriptorpb.FileDescriptorProto) *descriptorpb.FileDescriptorProto {
	output := proto.Clone(input).(*descriptorpb.FileDescriptorProto)
	output.Name = proto.String(stablePath(input.GetName()))

	// Pre-rewrite the imports in Phase 1 so they point to the other stripped files
	// in the new output set, rather than colliding with the source files.
	// record the imports so cleanup can happen later
	for i, dependency := range output.Dependency {
		output.Dependency[i] = stablePath(dependency)
	}

	output.MessageType = nil
	output.EnumType = nil
	output.Service = nil

	for _, msg := range input.MessageType {
		if filterMessage(msg) {
			continue
		}
		output.MessageType = append(output.MessageType, stripMessage(msg, input.GetPackage(), ""))
	}
	for _, enum := range input.EnumType {
		if filterEnumType(enum) {
			continue
		}
		output.EnumType = append(output.EnumType, stripEnum(enum))
	}
	for _, srv := range input.Service {
		if filterService(srv) {
			continue
		}
		output.Service = append(output.Service, stripService(srv))
	}
	// handle Extensions and Extension Fields - probably not needed/maybe should fail, whats the use-case of an experimental extension??
	// We do not clear output.Extension because we do not support stripping them;
	// the validation phase ensures no extensions are marked experimental, so we just keep them all.

	// T2: SourceCodeInfo isn't valid at this point - message_type/enum_type/
	// service just got renumbered (or dropped some elements) without it
	// being updated. It's intentionally left unset here rather than carrying
	// over the (now wrong) clone from above; main.go rebuilds it from
	// scratch once the whole stripped set is linked, via
	// rebuildSourceCodeInfo in sourceinfo.go
	output.SourceCodeInfo = nil
	return output
}

// stripMessage strips msg's experimental fields/nested types/enums. pkg and
// prefix are msg's own package/nesting context (mirroring catalog.go's
// indexMessage) - needed only to compute msg's fully-qualified name for
// mapEntryFieldSurvives, which must compare a field's TypeName (always fully
// qualified) against a nested map-entry's fully qualified name, not its bare
// short name (T12 follow-up, worklist Aug 17: a suffix-only comparison could
// false-match an unrelated field whose type happens to share the entry's
// bare name). SourceCodeInfo is handled separately, post-link, by
// rebuildSourceCodeInfo - this function only needs to get the structural
// (field/type) filtering right.
func stripMessage(msg *descriptorpb.DescriptorProto, pkg, prefix string) *descriptorpb.DescriptorProto {
	out := proto.Clone(msg).(*descriptorpb.DescriptorProto)
	out.Field = nil
	out.NestedType = nil
	out.EnumType = nil
	out.OneofDecl = nil
	// keep out.Extension as-is because we do not support stripping nested extensions.

	oneofReferenced := make([]bool, len(msg.OneofDecl))
	for _, field := range msg.Field {
		if filterField(field) || field.OneofIndex == nil {
			continue
		}
		oneofReferenced[field.GetOneofIndex()] = true
	}

	// oneofRemap re-points surviving fields' OneofIndex at their oneof's new
	// (renumbered) slot - structural, unrelated to SourceCodeInfo.
	oneofRemap := make(map[int32]int32, len(msg.OneofDecl))
	var newOneofIdx int32
	for oldIdx, oneof := range msg.OneofDecl {
		if !oneofReferenced[oldIdx] {
			continue
		}
		oneofRemap[int32(oldIdx)] = newOneofIdx
		out.OneofDecl = append(out.OneofDecl, proto.Clone(oneof).(*descriptorpb.OneofDescriptorProto))
		newOneofIdx++
	}

	for _, field := range msg.Field {
		if filterField(field) {
			continue
		}
		clonedField := proto.Clone(field).(*descriptorpb.FieldDescriptorProto)
		if clonedField.OneofIndex != nil {
			clonedField.OneofIndex = proto.Int32(oneofRemap[field.GetOneofIndex()])
		}
		out.Field = append(out.Field, clonedField)
	}

	msgFullName := fullProtoName(pkg, joinProtoName(prefix, msg.GetName()))
	for _, nested := range msg.NestedType {
		if filterMessage(nested) {
			continue
		}
		if nested.GetOptions().GetMapEntry() && !mapEntryFieldSurvives(msg, nested, msgFullName) {
			continue
		}
		out.NestedType = append(out.NestedType, stripMessage(nested, pkg, joinProtoName(prefix, msg.GetName())))
	}

	for _, enum := range msg.EnumType {
		if filterEnumType(enum) {
			continue
		}
		out.EnumType = append(out.EnumType, stripEnum(enum))
	}

	return out
}

func mapEntryFieldSurvives(msg *descriptorpb.DescriptorProto, nested *descriptorpb.DescriptorProto, msgFullName string) bool {
	nestedFullName := msgFullName + "." + nested.GetName()
	for _, field := range msg.Field {
		if field.GetTypeName() == nestedFullName {
			return !filterField(field)
		}
	}
	return true
}

// stripEnum strips enum's experimental values.
func stripEnum(enum *descriptorpb.EnumDescriptorProto) *descriptorpb.EnumDescriptorProto {
	out := proto.Clone(enum).(*descriptorpb.EnumDescriptorProto)
	out.Value = nil

	for _, value := range enum.Value {
		if filterEnumValue(value) {
			continue
		}
		out.Value = append(out.Value, proto.Clone(value).(*descriptorpb.EnumValueDescriptorProto))
	}
	return out
}

// stripService strips srv's experimental methods.
func stripService(srv *descriptorpb.ServiceDescriptorProto) *descriptorpb.ServiceDescriptorProto {
	out := proto.Clone(srv).(*descriptorpb.ServiceDescriptorProto)
	out.Method = nil

	for _, method := range srv.Method {
		if filterMethod(method) {
			continue
		}
		out.Method = append(out.Method, proto.Clone(method).(*descriptorpb.MethodDescriptorProto))
	}
	return out
}

func isExperimental(options proto.Message, extTag protowire.Number) bool {
	if options == nil || reflect.ValueOf(options).IsNil() {
		return false
	}

	found := false
	options.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		found = field.IsExtension() && field.Number() == protoreflect.FieldNumber(extTag)
		return !found
	})
	if found {
		return true
	}

	unknown := options.ProtoReflect().GetUnknown()
	for len(unknown) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(unknown)
		if tagLength < 0 {
			return false
		}
		unknown = unknown[tagLength:]
		if number == extTag && wireType == protowire.BytesType {
			return true
		}
		valueLength := protowire.ConsumeFieldValue(number, wireType, unknown)
		if valueLength < 0 {
			return false
		}
		unknown = unknown[valueLength:]
	}
	return false
}

func sanitizeImports(input *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	catalog := newCatalog(input)
	for _, file := range input.File {
		if !isStableAPIPath(file.GetName()) {
			continue
		}

		imports := catalog.importsFor(file)
		file.Dependency = imports.direct
		file.PublicDependency = imports.public
		file.WeakDependency = imports.weak
	}
	return input
}

func stablePath(path string) string {
	return strings.Replace(path, apiNextPrefix, stableAPIPrefix, 1)
}
