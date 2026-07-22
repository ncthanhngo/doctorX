import SwiftUI

struct VolumeDetailView: View {
    let state: AppState
    let partition: Partition
    @Binding var showQuickRestore: Bool

    @State private var selection = Set<String>()

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header

            Divider()

            if let result = state.scanResult {
                if !result.usedRawDriver {
                    Banner(
                        icon: "info.circle",
                        tint: .orange,
                        title: "Quét ở chế độ hạn chế",
                        detail: "Chưa có driver riêng cho \(partition.fsType.uppercased()), nên chỉ phát hiện được cờ Hidden. "
                              + "Bạn vẫn sao chép dữ liệu ra nơi an toàn được, nhưng chưa gỡ cờ ẩn tại chỗ."
                    )
                }
                if !result.findings.isEmpty {
                    findingsSection(result)
                }
                concealedList(result)
            } else {
                ContentUnavailableView("Đang chuẩn bị...", systemImage: "hourglass")
                    .frame(maxHeight: .infinity)
            }

            Divider()
            footer
        }
        .navigationTitle(partition.displayName)
    }

    private var header: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 4) {
                Text(partition.displayName).font(.title2.bold())
                Text("\(partition.fsType.uppercased()) · \(formatBytes(partition.sizeBytes)) · \(partition.bsd)")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                Task { await state.scan() }
            } label: {
                Label("Quét lại", systemImage: "arrow.clockwise")
            }
        }
        .padding()
    }

    private func findingsSection(_ result: ScanResult) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Banner(
                icon: "exclamationmark.shield",
                tint: result.severity == "likely-infected" ? .red : .orange,
                title: result.severityLabel,
                detail: "Đây là nhận định dựa trên dấu hiệu cấu trúc, không phải kết quả quét virus."
            )
            ForEach(result.findings) { f in
                VStack(alignment: .leading, spacing: 2) {
                    Text(f.path).font(.callout.monospaced())
                    Text(f.reason).font(.caption).foregroundStyle(.secondary)
                }
                .padding(.horizontal)
            }
        }
        .padding(.vertical, 8)
    }

    private func concealedList(_ result: ScanResult) -> some View {
        let items = result.userData
        return Group {
            if items.isEmpty {
                ContentUnavailableView(
                    "Không có dữ liệu nào bị giấu",
                    systemImage: "checkmark.circle",
                    description: Text("Ổ này không có file hay thư mục nào bị đánh dấu ẩn.")
                )
                .frame(maxHeight: .infinity)
            } else {
                Table(items, selection: $selection) {
                    TableColumn("Tên") { item in
                        Label(item.name, systemImage: item.isDir ? "folder" : "doc")
                    }
                    TableColumn("Đường dẫn") { Text($0.path).foregroundStyle(.secondary) }
                    TableColumn("Kích thước") { Text($0.isDir ? "—" : formatBytes($0.size)) }
                    TableColumn("Trạng thái") { Text($0.attrs.joined(separator: ", ")) }
                }
            }
        }
    }

    private var footer: some View {
        HStack {
            if let summary = state.lastRescueSummary {
                Label(summary, systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                    .font(.callout)
            }
            Spacer()

            Button {
                Task { await state.copyOut(paths: Array(selection), dest: nil) }
            } label: {
                Label("Sao chép ra nơi an toàn", systemImage: "arrow.down.doc")
            }
            .disabled(selection.isEmpty)
            .help("Chép dữ liệu sang ổ hệ thống. Không ghi gì lên ổ nguồn.")

            Button {
                showQuickRestore = true
            } label: {
                Label("Khôi phục thư mục ẩn", systemImage: "eye")
            }
            .buttonStyle(.borderedProminent)
            .disabled(state.scanResult?.usedRawDriver != true)
        }
        .padding()
    }
}

struct Banner: View {
    let icon: String
    let tint: Color
    let title: String
    let detail: String

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: icon).foregroundStyle(tint)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.callout.bold())
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding()
        .background(tint.opacity(0.1))
    }
}
