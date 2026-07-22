import SwiftUI

struct VolumeDetailView: View {
    let state: AppState
    let partition: Partition
    @Binding var showQuickRestore: Bool

    @State private var selection = Set<String>()

    private var result: ScanResult? { state.scanResult }
    private var severity: SeverityStyle {
        SeverityStyle(severity: result?.severity ?? "clean")
    }

    var body: some View {
        VStack(spacing: 0) {
            hero
            ScrollView {
                VStack(spacing: 16) {
                    if let result {
                        if !result.usedRawDriver { limitedBanner }
                        statCards(result)
                        if !result.findings.isEmpty { findingsCard(result) }
                        concealedCard(result)
                    } else {
                        ProgressView("Đang chuẩn bị...").frame(maxWidth: .infinity, minHeight: 200)
                    }
                }
                .padding(20)
            }
            actionBar
        }
    }

    // MARK: Hero header

    private var hero: some View {
        VStack(spacing: 14) {
            HStack(alignment: .center, spacing: 14) {
                ZStack {
                    RoundedRectangle(cornerRadius: 12).fill(.white.opacity(0.2))
                    Image(systemName: "internaldrive.fill").font(.title).foregroundStyle(.white)
                }
                .frame(width: 54, height: 54)

                VStack(alignment: .leading, spacing: 3) {
                    Text(partition.displayName).font(.title2.bold()).foregroundStyle(.white)
                    HStack(spacing: 8) {
                        Text(partition.fsType.uppercased()).fontWeight(.semibold)
                        Text("•")
                        Text(formatBytes(partition.sizeBytes))
                        Text("•")
                        Text(partition.bsd)
                    }
                    .font(.caption).foregroundStyle(.white.opacity(0.85))
                }
                Spacer()
                Button {
                    Task { await state.scan() }
                } label: {
                    Label("Quét lại", systemImage: "arrow.clockwise")
                        .font(.callout.weight(.medium))
                        .padding(.horizontal, 12).padding(.vertical, 7)
                        .background(.white.opacity(0.22), in: Capsule())
                        .foregroundStyle(.white)
                }
                .buttonStyle(.plain)
            }

            // Dải trạng thái lớn, đổi màu theo mức nghiêm trọng.
            HStack(spacing: 12) {
                Image(systemName: severity.icon).font(.title2).foregroundStyle(.white)
                VStack(alignment: .leading, spacing: 1) {
                    Text(severity.title).font(.headline).foregroundStyle(.white)
                    Text(statusSubtitle).font(.caption).foregroundStyle(.white.opacity(0.9))
                }
                Spacer()
            }
            .padding(12)
            .background(.white.opacity(0.15), in: RoundedRectangle(cornerRadius: 12))
        }
        .padding(20)
        .background(Brand.statusGradient(severity.tint).ignoresSafeArea(edges: .top))
    }

    private var statusSubtitle: String {
        guard let r = result else { return "Đang quét..." }
        let n = r.userData.count
        if n == 0 { return "Không có dữ liệu nào bị giấu trên ổ này." }
        return "\(n) mục bị giấu • \(r.findings.count) dấu hiệu nghi ngờ"
    }

    // MARK: Stat cards

    private func statCards(_ r: ScanResult) -> some View {
        HStack(spacing: 12) {
            StatCard(value: "\(r.userData.count)", label: "Mục bị giấu",
                     icon: "eye.slash.fill", tint: Brand.primary)
            StatCard(value: "\(r.userData.filter { $0.isDir }.count)", label: "Thư mục",
                     icon: "folder.fill", tint: Brand.accent)
            StatCard(value: "\(r.findings.count)", label: "Cảnh báo",
                     icon: "exclamationmark.shield.fill",
                     tint: r.findings.isEmpty ? Brand.safe : Brand.warn)
        }
    }

    // MARK: Findings

    private func findingsCard(_ r: ScanResult) -> some View {
        SectionCard(title: "Dấu hiệu nghi ngờ", systemImage: "shield.lefthalf.filled") {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(r.findings) { f in
                    HStack(alignment: .top, spacing: 10) {
                        Image(systemName: "exclamationmark.triangle.fill")
                            .foregroundStyle(Brand.warn).font(.callout)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(f.path).font(.callout.weight(.medium))
                            Text(f.reason).font(.caption).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                        Spacer()
                    }
                    if f.id != r.findings.last?.id { Divider() }
                }
                Text("Đây là nhận định theo dấu hiệu cấu trúc, không phải kết quả quét virus.")
                    .font(.caption2).foregroundStyle(.tertiary).padding(.top, 2)
            }
        }
    }

    // MARK: Concealed list

    private func concealedCard(_ r: ScanResult) -> some View {
        let items = r.userData
        return SectionCard(title: "Dữ liệu bị giấu", systemImage: "eye.slash") {
            if items.isEmpty {
                HStack(spacing: 10) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(Brand.safe).font(.title3)
                    Text("Không có file hay thư mục nào bị đánh dấu ẩn.").foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.vertical, 8)
            } else {
                VStack(spacing: 0) {
                    ForEach(items) { item in
                        ConcealedRow(
                            item: item,
                            checked: selection.contains(item.path),
                            toggle: { toggle(item.path) }
                        )
                        if item.id != items.last?.id { Divider() }
                    }
                }
            }
        }
    }

    private func toggle(_ path: String) {
        if selection.contains(path) { selection.remove(path) } else { selection.insert(path) }
    }

    // MARK: Limited driver banner

    private var limitedBanner: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "info.circle.fill").foregroundStyle(Brand.accent)
            VStack(alignment: .leading, spacing: 2) {
                Text("Quét ở chế độ hạn chế").font(.callout.bold())
                Text("Chưa có driver riêng cho \(partition.fsType.uppercased()); chỉ phát hiện được cờ Hidden. Vẫn sao chép dữ liệu ra nơi an toàn được.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(14)
        .background(Brand.accent.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
    }

    // MARK: Action bar

    private var actionBar: some View {
        HStack(spacing: 12) {
            if let s = state.lastRescueSummary {
                Label(s, systemImage: "checkmark.circle.fill")
                    .foregroundStyle(Brand.safe).font(.callout).lineLimit(1)
            }
            Spacer()
            BrandButton(title: selection.isEmpty ? "Sao chép ra nơi an toàn"
                        : "Sao chép \(selection.count) mục", icon: "arrow.down.doc.fill",
                        tint: Brand.accent, filled: false) {
                Task { await state.copyOut(paths: Array(selection), dest: nil) }
            }
            .frame(width: 240)
            .disabled(selection.isEmpty)
            .opacity(selection.isEmpty ? 0.5 : 1)

            BrandButton(title: "Khôi phục thư mục ẩn", icon: "eye.fill", tint: Brand.primary) {
                showQuickRestore = true
            }
            .frame(width: 220)
            .disabled(result?.usedRawDriver != true)
            .opacity(result?.usedRawDriver == true ? 1 : 0.5)
        }
        .padding(16)
        .background(.ultraThinMaterial)
        .overlay(Divider(), alignment: .top)
    }
}

/// Một dòng file/thư mục bị giấu, có checkbox chọn để cứu.
struct ConcealedRow: View {
    let item: ConcealedItem
    let checked: Bool
    let toggle: () -> Void

    var body: some View {
        Button(action: toggle) {
            HStack(spacing: 11) {
                Image(systemName: checked ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(checked ? Brand.primary : Color.secondary.opacity(0.5))
                    .font(.title3)
                Image(systemName: item.isDir ? "folder.fill" : "doc.fill")
                    .foregroundStyle(item.isDir ? Brand.warn : Brand.accent)
                VStack(alignment: .leading, spacing: 1) {
                    Text(item.name).fontWeight(.medium).lineLimit(1)
                    Text(item.path).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                if !item.isDir {
                    Text(formatBytes(item.size)).font(.caption).foregroundStyle(.secondary)
                }
                ForEach(item.attrs, id: \.self) { a in
                    Text(a).font(.caption2.weight(.semibold)).foregroundStyle(Brand.warn)
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(Brand.warn.opacity(0.12), in: RoundedRectangle(cornerRadius: 4))
                }
            }
            .padding(.vertical, 7)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}
