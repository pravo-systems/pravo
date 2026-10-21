// Command protogen compiles the provider plugin protocol .proto files into Go
// message types without requiring a protoc binary. gRPC client stubs are
// hand-written in the tfplugin5/tfplugin6 packages, so only the message code
// is generated here.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/cmd/protoc-gen-go/internal_gengo"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: protogen <proto-dir> <file.proto> <out-dir>")
		os.Exit(2)
	}
	protoDir, file, outDir := os.Args[1], os.Args[2], os.Args[3]
	pkg := filepath.Base(outDir)

	comp := protocompile.Compiler{
		Resolver:       protocompile.WithStandardImports(&protocompile.SourceResolver{ImportPaths: []string{protoDir}}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := comp.Compile(context.Background(), file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compile:", err)
		os.Exit(1)
	}
	var fdps []*descriptorpb.FileDescriptorProto
	seen := map[string]bool{}
	var add func(f protoreflect.FileDescriptor)
	add = func(f protoreflect.FileDescriptor) {
		if seen[f.Path()] {
			return
		}
		seen[f.Path()] = true
		imps := f.Imports()
		for i := 0; i < imps.Len(); i++ {
			add(imps.Get(i).FileDescriptor)
		}
		fdps = append(fdps, protodesc.ToFileDescriptorProto(f))
	}
	for _, f := range files {
		add(f)
	}

	param := fmt.Sprintf("M%s=pravo.dev/pravo/internal/tfplugin/%s;%s", file, pkg, pkg)
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{file},
		Parameter:      proto.String(param),
		ProtoFile:      fdps,
	}
	gen, err := protogen.Options{}.New(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	for _, f := range gen.Files {
		if f.Generate {
			internal_gengo.GenerateFile(gen, f)
		}
	}
	resp := gen.Response()
	if resp.Error != nil {
		fmt.Fprintln(os.Stderr, "generate:", *resp.Error)
		os.Exit(1)
	}
	for _, f := range resp.File {
		out := filepath.Join(outDir, filepath.Base(f.GetName()))
		if err := os.WriteFile(out, []byte(f.GetContent()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", out)
	}
}
