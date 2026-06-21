# Homebrew formula for zarlcode

# To publish:
#   1. Create a repo: github.com/zarldev/homebrew-tap
#   2. Copy this file to: Formula/zarlcode.rb
#   3. Update `url` and `sha256` for each release
#   4. Users: `brew install zarldev/tap/zarlcode`
#
# The SHA256 can be pulled from the release's checksums.txt.
# Example for v0.1.2 linux/amd64:
#   curl -sL https://github.com/zarldev/zarlmono/releases/download/zarlcode/v0.1.2/checksums.txt | grep linux_amd64

class Zarlcode < Formula
  desc "Terminal coding agent / TUI — plan first, execute second, rewind anytime"
  homepage "https://github.com/zarldev/zarlmono"
  version "0.1.2"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/zarldev/zarlmono/releases/download/zarlcode/v0.1.2/zarlcode_v0.1.2_darwin_arm64.tar.gz"
      sha256 "415ac24eec8607bafbb04342598ce87675d869d57dc593133b4d2f1baeba1a59"    else
      url "https://github.com/zarldev/zarlmono/releases/download/zarlcode/v0.1.2/zarlcode_v0.1.2_darwin_amd64.tar.gz"
      sha256 "9b0bece728bd6ce475500547ace6604e274df2770d721da0ac06d085a4bec4c4"    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/zarldev/zarlmono/releases/download/zarlcode/v0.1.2/zarlcode_v0.1.2_linux_arm64.tar.gz"
      sha256 "3100802c54dbbaff2fa1214f360cc9201824a7c531b00549a972a375ccae5c19"    else
      url "https://github.com/zarldev/zarlmono/releases/download/zarlcode/v0.1.2/zarlcode_v0.1.2_linux_amd64.tar.gz"
      sha256 "943778686fad56845b1f6a4b5f513f7bce1a0b6573487881dd8d1d880d0f216a"    end
  end

  def install
    bin.install "zarlcode"
  end

  test do
    system "#{bin}/zarlcode", "-version"
  end
end
