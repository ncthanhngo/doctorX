// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "DoctorX",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "DoctorX",
            path: "Sources/DoctorX"
        )
    ]
)
