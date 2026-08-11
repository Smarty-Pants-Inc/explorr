# typed: false
# frozen_string_literal: true

class Explorr < Formula
  desc "Mouse-first terminal code editor with LSP diagnostics"
  homepage "https://github.com/Smarty-Pants-Inc/explorr"
  version "1.0.3"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.3/explorr_1.0.3_darwin_arm64.tar.gz"
      sha256 "659909862d02cc6d91c96ddc376b0e97324b0fb1c29969108160cf89d76a5eb4"
    else
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.3/explorr_1.0.3_darwin_amd64.tar.gz"
      sha256 "fd21ecf859b602634a09fddb7615cd6885bbd17cb5a7df346e22880a38da90a9"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.3/explorr_1.0.3_linux_arm64.tar.gz"
      sha256 "1d703748be74a326b67f6356d40ed61d807071edfd2b300c0d7324233e5c867d"
    else
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.3/explorr_1.0.3_linux_amd64.tar.gz"
      sha256 "fb5fb95d3e3cc5ffc3607f7b4f39b3b54caa38c98a8b994261f741d65dc0bf79"
    end
  end

  def install
    bin.install "explorr"
  end

  test do
    assert_match "explorr #{version}", shell_output("#{bin}/explorr --version")
  end
end
