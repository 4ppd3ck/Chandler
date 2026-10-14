package ask

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"chandler/internal/config"
	"chandler/internal/term"
)

func AskStackCloud(root string, rd *term.Reader) (asked bool, err error) {
	if !term.IsTTY(os.Stdin.Fd()) {
		fmt.Println("Stack/cloud selection needs a terminal — keeping existing config.")
		return false, nil
	}
	if rd == nil {
		rd = term.NewReaderStdin()
	}

	sel, err := (&MultiSelect{
		Title:  "Which Kubernetes stack(s) do you use? (Space to check, Enter to accept)",
		Prompt: "k8s> ",
		Items:  K8sStackItems,
	}).Run(rd)
	if err != nil {
		if errors.Is(err, ErrCancelled) {
			fmt.Println("Cancelled — config unchanged.")
			return false, nil
		}
		return false, err
	}
	stacks := TokensFromSelection(sel)
	if stacks == "" {
		stacks = "rancher"
	}

	sel, err = (&MultiSelect{
		Title:  "Which cloud provider(s) do you use? (Space to check, Enter to accept)",
		Prompt: "cloud> ",
		Items:  CloudItems,
	}).Run(rd)
	if err != nil {
		if errors.Is(err, ErrCancelled) {
			fmt.Println("Cancelled — config unchanged.")
			return false, nil
		}
		return false, err
	}
	clouds := TokensFromSelection(sel)
	if clouds == "" {
		clouds = "none"
	}

	if err := config.Save(root, stacks, clouds); err != nil {
		return true, err
	}
	fmt.Printf("Saved: k8s stack(s) = %s · cloud = %s  (%s)\n",
		stacks, clouds, filepath.Join(root, "config.env"))
	PrintStackNotes()
	return true, nil
}
