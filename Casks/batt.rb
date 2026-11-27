cask "batt" do
  version "0.10.0"
  sha256 "c15f905a92e7f7953bcc99b40359c4af72a99e07be77f588c0306a2a9932975b"

  url "https://github.com/charlie0129/batt/releases/download/v#{version}/batt-v#{version}.dmg"
  name "Batt"
  desc "Menu bar app to control battery charging on Apple Silicon MacBooks"
  homepage "https://github.com/charlie0129/batt"

  depends_on arch: :arm64
  depends_on macos: :ventura

  app "batt.app"
end
