package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jhoblitt/gnome-monitor-pin/internal/cli"
)

var _ = Describe("Run", func() {
	It("greets the name it is given", func(ctx SpecContext) {
		var stdout, stderr bytes.Buffer
		err := cli.Run(ctx, []string{"--name", "gnome"}, nil, &stdout, &stderr)
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout.String()).To(Equal("hello, gnome\n"))
	})
})
