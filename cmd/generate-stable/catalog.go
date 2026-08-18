package main

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// extensionKey identifies an extension by (extendee, number) rather than by
// name: numbers are unique only per extendee, not globally, and once an
// extension isn't Go-registered in this binary, raw wire bytes expose only
// its number — never its FQN — so a name-based key wouldn't be recoverable
// in the common case anyway.
type extensionKey struct {
	extendee string
	number   int32
}

// catalog records where the source schema declares every type and extension.
// Selection never needs to mutate this index.
type catalog struct {
	typeOwner      map[string]string
	extensionOwner map[extensionKey]string
}

// catalog tracks the ownership of types
func newCatalog(input *descriptorpb.FileDescriptorSet) catalog {
	catalog := catalog{
		typeOwner:      make(map[string]string),
		extensionOwner: make(map[extensionKey]string),
	}
	for _, file := range input.File {
		for _, message := range file.MessageType {
			catalog.indexMessage(file.GetName(), file.GetPackage(), "", message)
		}
		for _, enum := range file.EnumType {
			catalog.indexEnum(file.GetName(), file.GetPackage(), "", enum)
		}
		for _, extension := range file.Extension {
			catalog.indexExtension(file.GetName(), extension)
		}
	}
	return catalog
}

func (c catalog) indexMessage(file, packageName, prefix string, message *descriptorpb.DescriptorProto) {
	name := joinProtoName(prefix, message.GetName())
	c.typeOwner[fullProtoName(packageName, name)] = file
	for _, extension := range message.Extension {
		c.indexExtension(file, extension)
	}
	for _, nested := range message.NestedType {
		c.indexMessage(file, packageName, name, nested)
	}
	for _, enum := range message.EnumType {
		c.indexEnum(file, packageName, name, enum)
	}
}

func (c catalog) indexEnum(file, packageName, prefix string, enum *descriptorpb.EnumDescriptorProto) {
	c.typeOwner[fullProtoName(packageName, joinProtoName(prefix, enum.GetName()))] = file
}

func (c catalog) indexExtension(file string, extension *descriptorpb.FieldDescriptorProto) {
	if extension.GetExtendee() == "" {
		return
	}
	c.extensionOwner[extensionKey{
		extendee: extension.GetExtendee(),
		number:   extension.GetNumber(),
	}] = file
}

func joinProtoName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func fullProtoName(packageName, name string) string {
	if packageName == "" {
		return "." + name
	}
	return "." + packageName + "." + name
}

type imports struct {
	direct []string
	public []int32
	weak   []int32
}

func (c catalog) importsFor(file *descriptorpb.FileDescriptorProto) imports {
	needed := make(map[string]bool)
	c.collectFileImports(file, needed)
	return retainImports(file, needed)
}

func retainImports(file *descriptorpb.FileDescriptorProto, needed map[string]bool) imports {
	publicOrWeak := make(map[int32]bool, len(file.PublicDependency)+len(file.WeakDependency))
	for _, dependency := range file.PublicDependency {
		publicOrWeak[dependency] = true
	}
	for _, dependency := range file.WeakDependency {
		publicOrWeak[dependency] = true
	}

	oldToNew := make(map[int32]int32, len(file.Dependency))
	retained := imports{direct: make([]string, 0, len(file.Dependency))}
	for oldIndex, dependency := range file.Dependency {
		if !needed[dependency] && !publicOrWeak[int32(oldIndex)] {
			continue
		}
		oldToNew[int32(oldIndex)] = int32(len(retained.direct))
		retained.direct = append(retained.direct, dependency)
	}
	retained.public = remapImportIndexes(file.PublicDependency, oldToNew)
	retained.weak = remapImportIndexes(file.WeakDependency, oldToNew)
	return retained
}

func remapImportIndexes(indexes []int32, oldToNew map[int32]int32) []int32 {
	mapped := make([]int32, 0, len(indexes))
	for _, oldIndex := range indexes {
		if newIndex, ok := oldToNew[oldIndex]; ok {
			mapped = append(mapped, newIndex)
		}
	}
	return mapped
}

func (c catalog) collectFileImports(file *descriptorpb.FileDescriptorProto, needed map[string]bool) {
	c.collectOptionImports(file.GetOptions(), file.GetName(), needed)
	for _, message := range file.MessageType {
		c.collectMessageImports(message, file.GetName(), needed)
	}
	for _, enum := range file.EnumType {
		c.collectEnumImports(enum, file.GetName(), needed)
	}
	for _, extension := range file.Extension {
		c.collectFieldImports(extension, file.GetName(), needed)
	}
	for _, service := range file.Service {
		c.collectServiceImports(service, file.GetName(), needed)
	}
}

func (c catalog) collectMessageImports(message *descriptorpb.DescriptorProto, file string, needed map[string]bool) {
	c.collectOptionImports(message.GetOptions(), file, needed)
	for _, field := range message.Field {
		c.collectFieldImports(field, file, needed)
	}
	for _, extension := range message.Extension {
		c.collectFieldImports(extension, file, needed)
	}
	for _, oneof := range message.OneofDecl {
		c.collectOptionImports(oneof.GetOptions(), file, needed)
	}
	for _, nested := range message.NestedType {
		c.collectMessageImports(nested, file, needed)
	}
	for _, enum := range message.EnumType {
		c.collectEnumImports(enum, file, needed)
	}
}

func (c catalog) collectEnumImports(enum *descriptorpb.EnumDescriptorProto, file string, needed map[string]bool) {
	c.collectOptionImports(enum.GetOptions(), file, needed)
	for _, value := range enum.Value {
		c.collectOptionImports(value.GetOptions(), file, needed)
	}
}

func (c catalog) collectFieldImports(field *descriptorpb.FieldDescriptorProto, file string, needed map[string]bool) {
	c.collectOptionImports(field.GetOptions(), file, needed)
	if field.GetTypeName() != "" {
		c.markTypeImport(field.GetTypeName(), file, needed)
	}
	if field.GetExtendee() != "" {
		c.markTypeImport(field.GetExtendee(), file, needed)
	}
}

func (c catalog) collectServiceImports(service *descriptorpb.ServiceDescriptorProto, file string, needed map[string]bool) {
	c.collectOptionImports(service.GetOptions(), file, needed)
	for _, method := range service.Method {
		c.collectOptionImports(method.GetOptions(), file, needed)
		c.markTypeImport(method.GetInputType(), file, needed)
		c.markTypeImport(method.GetOutputType(), file, needed)
	}
}

func (c catalog) collectOptionImports(options proto.Message, file string, needed map[string]bool) {
	if options == nil {
		return
	}
	extendee := "." + string(options.ProtoReflect().Descriptor().FullName())
	options.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if field.IsExtension() {
			c.markExtensionImport(extendee, int32(field.Number()), file, needed)
		}
		return true
	})

	// now, consume the custom extensions
	unknown := options.ProtoReflect().GetUnknown()
	for len(unknown) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(unknown)
		if tagLength < 0 {
			return
		}
		unknown = unknown[tagLength:]
		c.markExtensionImport(extendee, int32(number), file, needed)
		valueLength := protowire.ConsumeFieldValue(number, wireType, unknown)
		if valueLength < 0 {
			return
		}
		unknown = unknown[valueLength:]
	}
}

func (c catalog) markExtensionImport(extendee string, number int32, file string, needed map[string]bool) {
	if dependency, ok := c.extensionOwner[extensionKey{extendee: extendee, number: number}]; ok && dependency != file {
		needed[dependency] = true
	}
}

func (c catalog) markTypeImport(typeName, file string, needed map[string]bool) {
	if typeName == "" {
		return
	}
	if dependency, ok := c.typeOwner[typeName]; ok && dependency != file {
		needed[dependency] = true
	}
}
