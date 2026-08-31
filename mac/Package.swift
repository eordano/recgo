// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "Recgo",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "Recgo",
            path: "Sources/Recgo"
        )
    ]
)
