// Comando mestre-virtual: CLI do mestre virtual de RPG de mesa.
//
// Usa apenas a stdlib (flag) porque o ambiente onde o projeto nasceu não
// tinha acesso a proxy.golang.org. Migrar para Cobra é trivial: cada
// subcomando abaixo já é uma função isolada.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rafvazl/mestre-virtual/internal/character"
	"github.com/rafvazl/mestre-virtual/internal/knowledge"
	"github.com/rafvazl/mestre-virtual/internal/llm"
	"github.com/rafvazl/mestre-virtual/internal/llm/factory"
	"github.com/rafvazl/mestre-virtual/internal/storage"
)

const usage = `mestre-virtual — mestre de RPG de mesa com LLM

Uso:
  mestre-virtual <comando> [flags]

Comandos:
  content            lista a base de conhecimento (content/)
  character create   cria um personagem a partir de uma classe
  character list     lista personagens salvos
  ask                envia um prompt ao LLM e mostra a resposta parseada
  version            mostra a versão

Flags globais (depois do comando):
  -content DIR   diretório da base de conhecimento (padrão: content)
  -data DIR      diretório de estado (padrão: data)
  -llm BACKEND   local | remote | routing (padrão: local)
`

var version = "0.2.0-dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

type globals struct {
	contentDir, dataDir, backend, model string
}

func newFlagSet(name string, g *globals) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&g.contentDir, "content", "content", "diretório da base de conhecimento")
	fs.StringVar(&g.dataDir, "data", "data", "diretório de estado")
	fs.StringVar(&g.backend, "llm", "local", "backend de LLM: local|remote|routing")
	fs.StringVar(&g.model, "model", "default", "modelo do backend remoto")
	return fs
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(out, version)
		return nil
	case "content":
		return cmdContent(args[1:], out)
	case "character":
		if len(args) < 2 {
			return errors.New("use: character create|list")
		}
		switch args[1] {
		case "create":
			return cmdCharacterCreate(args[2:], out)
		case "list":
			return cmdCharacterList(args[2:], out)
		}
		return fmt.Errorf("subcomando desconhecido: character %s", args[1])
	case "ask":
		return cmdAsk(args[1:], out)
	}
	return fmt.Errorf("comando desconhecido %q (veja: mestre-virtual help)", args[0])
}

func cmdContent(args []string, out io.Writer) error {
	var g globals
	if err := newFlagSet("content", &g).Parse(args); err != nil {
		return err
	}
	ks, err := knowledge.NewFileStore(g.contentDir)
	if err != nil {
		return err
	}
	for _, t := range []knowledge.RefType{knowledge.RefClass, knowledge.RefItem, knowledge.RefEnvironment, knowledge.RefNPC} {
		fmt.Fprintf(out, "%s: %s\n", t, strings.Join(ks.List(t), ", "))
	}
	return nil
}

func cmdCharacterCreate(args []string, out io.Writer) error {
	var g globals
	fs := newFlagSet("character create", &g)
	id := fs.String("id", "", "ID do personagem (obrigatório)")
	name := fs.String("name", "", "nome do personagem (obrigatório)")
	class := fs.String("class", "", "ID da classe (obrigatório)")
	player := fs.String("player", "", "nome do jogador")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *name == "" || *class == "" {
		return errors.New("-id, -name e -class são obrigatórios")
	}
	ks, err := knowledge.NewFileStore(g.contentDir)
	if err != nil {
		return err
	}
	cl, err := ks.Class(*class)
	if err != nil {
		return err
	}
	st, err := storage.NewFileStore(g.dataDir)
	if err != nil {
		return err
	}
	if _, err := st.LoadCharacter(*id); err == nil {
		return fmt.Errorf("personagem %q já existe", *id)
	}
	c := character.New(*id, *name, cl)
	c.Player = *player
	if err := st.SaveCharacter(c); err != nil {
		return err
	}
	fmt.Fprintf(out, "criado %s (%s) PV %d/%d itens %v\n", c.Name, cl.Name, c.HP, c.MaxHP, c.Items())
	return nil
}

func cmdCharacterList(args []string, out io.Writer) error {
	var g globals
	if err := newFlagSet("character list", &g).Parse(args); err != nil {
		return err
	}
	st, err := storage.NewFileStore(g.dataDir)
	if err != nil {
		return err
	}
	ids, err := st.ListCharacters()
	if err != nil {
		return err
	}
	for _, id := range ids {
		c, err := st.LoadCharacter(id)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s\t%s\t%s\tPV %d/%d\n", c.ID, c.Name, c.ClassID, c.HP, c.MaxHP)
	}
	return nil
}

func cmdAsk(args []string, out io.Writer) error {
	var g globals
	fs := newFlagSet("ask", &g)
	if err := fs.Parse(args); err != nil {
		return err
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" {
		return errors.New("informe o prompt: mestre-virtual ask <texto>")
	}
	p, err := factory.New(factory.Config{Backend: g.backend, RemoteModel: g.model})
	if err != nil {
		return err
	}
	resp, err := p.Generate(context.Background(), llm.Request{Prompt: prompt})
	if err != nil {
		return err
	}
	parsed, perrs := llm.ParseResponse(resp.Text)
	fmt.Fprintf(out, "[%s]\n%s\n", resp.Provider, parsed.Narrative)
	for _, e := range parsed.Effects {
		fmt.Fprintf(out, "efeito: %+v\n", e)
	}
	for _, e := range perrs {
		fmt.Fprintf(out, "ignorado: %v\n", e)
	}
	return nil
}
