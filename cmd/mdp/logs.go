package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show daemon log output",
	RunE:  runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolP("follow", "f", false, "Follow log output (tail -f)")
	logsCmd.Flags().IntP("lines", "n", 50, "Number of lines (0 for all)")
}

func runLogs(cmd *cobra.Command, args []string) error {
	follow, _ := cmd.Flags().GetBool("follow")
	lines, _ := cmd.Flags().GetInt("lines")

	logPath := logFilePath()
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return fmt.Errorf("no log file found at %s — is the daemon running?", logPath)
	}

	if follow {
		return tailFollow(logPath, lines)
	}
	return tailLines(logPath, lines)
}

func tailLines(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if n <= 0 {
		_, err := io.Copy(os.Stdout, f)
		return err
	}

	allLines, err := readAllLines(f)
	if err != nil {
		return err
	}

	start := len(allLines) - n
	if start < 0 {
		start = 0
	}
	for _, line := range allLines[start:] {
		fmt.Println(line)
	}
	return nil
}

func tailFollow(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if n > 0 {
		allLines, err := readAllLines(f)
		if err != nil {
			return err
		}
		start := len(allLines) - n
		if start < 0 {
			start = 0
		}
		for _, line := range allLines[start:] {
			fmt.Println(line)
		}
		// seek to end for following
		f.Seek(0, io.SeekEnd)
	} else {
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return err
		}
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(line)
		}
		if err != nil {
			if err == io.EOF {
				// poll — reopen isn't necessary since we keep the fd
				continue
			}
			return err
		}
	}
}

func readAllLines(f *os.File) ([]string, error) {
	f.Seek(0, io.SeekStart)
	var lines []string
	scanner := bufio.NewScanner(f)
	maxBuf := 1024 * 1024
	scanner.Buffer(make([]byte, 0, maxBuf), maxBuf)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

