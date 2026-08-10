# typed: false
# frozen_string_literal: true

class Explorr < Formula
  desc "Mouse-first terminal code editor with LSP diagnostics"
  homepage "https://github.com/Smarty-Pants-Inc/explorr"
  version "1.0.1"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.1/explorr_1.0.1_darwin_arm64.tar.gz"
      sha256 "4347ccc7fcfcb0587bea1793eff59a048f1fd3609d444e7f1ba3fcc2299e9111"
    else
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.1/explorr_1.0.1_darwin_amd64.tar.gz"
      sha256 "a9ce017b32d5b76f2591fe8009b6617b29ef6852645c5e5130ec3abc9502549f"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.1/explorr_1.0.1_linux_arm64.tar.gz"
      sha256 "b91abc07f567926f55d96146272569f721f0337451118b78979dbab8a56d2bf3"
    else
      url "https://github.com/Smarty-Pants-Inc/explorr/releases/download/v1.0.1/explorr_1.0.1_linux_amd64.tar.gz"
      sha256 "ad665ab19f1d6297ab6640aed8c420b98b72698aa0a96e5472aafc4303fa73dc"
    end
  end

  def install
    bin.install "explorr"
  end

  test do
    assert_match "explorr #{version}", shell_output("#{bin}/explorr --version")
  end
end
