# typed: false
# frozen_string_literal: true

class Explorr < Formula
  desc "Mouse-first terminal code editor with LSP diagnostics"
  homepage "https://github.com/Smarty-Pants-Inc/explorr"
  version "1.0.2"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.2/explorr_1.0.2_darwin_arm64.tar.gz"
      sha256 "e671e92787cbe8bef53c0b4c0be88b687a97a12c1bffb10e88592a383af6afae"
    else
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.2/explorr_1.0.2_darwin_amd64.tar.gz"
      sha256 "a3ad12f1b995660bf41a7198bcd38608c5a9fa0d7f6e1492a5c8bdc4bde1e3f9"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.2/explorr_1.0.2_linux_arm64.tar.gz"
      sha256 "80ece520ec026a4d6b4e56004e3819b038d058caf4774478d7b3c09a22a4e19a"
    else
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.2/explorr_1.0.2_linux_amd64.tar.gz"
      sha256 "97b7d6e81b86facfc9d11b28ff17886c2a0efa378050a751c769d22cbc0b9e68"
    end
  end

  def install
    bin.install "explorr"
  end

  test do
    assert_match "explorr #{version}", shell_output("#{bin}/explorr --version")
  end
end
