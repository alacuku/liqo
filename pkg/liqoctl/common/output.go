package common

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/alacuku/pterm"
	"io"
	"strings"
)

var (
	GenericPrinter = pterm.PrefixPrinter{
		Prefix: pterm.Prefix{},
		Scope: pterm.Scope{
			Text:  "generic",
			Style: pterm.NewStyle(pterm.FgGray),
		},
		MessageStyle: pterm.NewStyle(pterm.FgDefault),
	}

	SuccessPrinter = GenericPrinter.WithPrefix(pterm.Prefix{
		Text:  "[SUCCESS]",
		Style: pterm.NewStyle(pterm.FgGreen),
	})

	WarningPrinter = GenericPrinter.WithPrefix(pterm.Prefix{
		Text:  "[WARNING]",
		Style: pterm.NewStyle(pterm.FgYellow),
	})

	ErrorPrinter = GenericPrinter.WithPrefix(pterm.Prefix{
		Text:  "[ERROR]",
		Style: pterm.NewStyle(pterm.FgRed),
	})
)

func ConcurrentSpinner(spinner1, spinner2 string) (io.Writer, chan bool){
	pipeReader, pipeWriter := io.Pipe()

	scanner := bufio.NewScanner(pipeReader)
	scanner.Split(scanLines)

	area, _ := pterm.DefaultArea.Start()
	stopCh := make(chan bool, 1)
	go func() {
		var prevSpinner1, prevSpinner2 string
		for  {
			select {
			case exit := <- stopCh:
				if exit{
					_ = area.Stop()
					_ = pipeWriter.Close()
					_ = pipeReader.Close()
					fmt.Println("closing c spinner")
					return
				}
			default:
				_ = scanner.Scan()
				line := scanner.Text()
				if strings.Contains(line, spinner1){
					line = strings.ReplaceAll(line, "\n", "")
					prevSpinner1 = line + "\n"
				} else if strings.Contains(line, spinner2){
					line = strings.ReplaceAll(line, "\n", "")
					prevSpinner2 = line
				}else{
					continue
				}
				area.Update(prevSpinner1, prevSpinner2)

			}


		}
	}()
	return pipeWriter, stopCh
}

func scanLines (data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\t'); i >= 0 {
		// We have a full newline-terminated line.
		return i + 1, data[0:i], nil
	}
	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}


