package main

import (
	"github.com/spf13/cobra"
)

// versionCmd 打印版本与构建信息。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "打印版本、Git 提交、构建时间与平台信息",
	RunE: func(cmd *cobra.Command, args []string) error {
		printVersion()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
