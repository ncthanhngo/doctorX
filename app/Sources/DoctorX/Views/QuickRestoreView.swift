import SwiftUI

/// Khôi phục thư mục bị giấu ở gốc ổ — bản chuyển của Khoi-phuc-thu-muc-an.bat.
/// Thư mục hệ thống không chọn được (khác script gốc: không chỉ cảnh báo).
struct QuickRestoreView: View {
    let state: AppState
    @Environment(\.dismiss) private var dismiss

    @State private var selectedPath: String?
    @State private var recursive = true
    @State private var confirming = false

    private var candidates: [ConcealedItem] {
        (state.scanResult?.concealed ?? []).filter(\.isDir)
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if candidates.isEmpty {
                emptyState
            } else {
                list
            }
            Divider()
            footer
        }
        .frame(width: 580, height: 520)
        .confirmationDialog(
            "Khôi phục “\(shortName(selectedPath))”?",
            isPresented: $confirming, titleVisibility: .visible
        ) {
            Button("Khôi phục") {
                if let path = selectedPath {
                    Task { await state.unhide(path: path, recursive: recursive); dismiss() }
                }
            }
            Button("Huỷ", role: .cancel) {}
        } message: {
            Text(recursive
                 ? "Toàn bộ thư mục con bên trong cũng sẽ được hiện lại."
                 : "Chỉ thư mục này được hiện lại.")
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            ZStack {
                RoundedRectangle(cornerRadius: 10).fill(Brand.heroGradient)
                Image(systemName: "eye.fill").foregroundStyle(.white)
            }
            .frame(width: 42, height: 42)
            VStack(alignment: .leading, spacing: 2) {
                Text("Khôi phục thư mục bị giấu").font(.title3.bold())
                Text("Gỡ cờ Hidden + System để Windows hiện lại thư mục")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(16)
    }

    private var list: some View {
        ScrollView {
            VStack(spacing: 8) {
                ForEach(candidates) { item in
                    RestoreRow(
                        item: item,
                        selected: selectedPath == item.path,
                        onTap: { if !item.isProtected { selectedPath = item.path } }
                    )
                }
            }
            .padding(14)
        }
    }

    private var emptyState: some View {
        VStack(spacing: 12) {
            Image(systemName: "checkmark.circle.fill").font(.system(size: 40)).foregroundStyle(Brand.safe)
            Text("Không có thư mục nào bị giấu ở gốc ổ").font(.headline)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var footer: some View {
        VStack(spacing: 12) {
            Toggle(isOn: $recursive) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("Áp dụng cho toàn bộ thư mục con bên trong")
                    Text("Tương đương attrib -h -s /s /d").font(.caption2).foregroundStyle(.secondary)
                }
            }
            .toggleStyle(.switch)
            .tint(Brand.primary)

            HStack {
                Label("Có thể hoàn tác sau khi khôi phục", systemImage: "arrow.uturn.backward")
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("Đóng") { dismiss() }
                BrandButton(title: "Khôi phục", icon: "eye.fill", tint: Brand.primary) {
                    confirming = true
                }
                .frame(width: 140)
                .disabled(selectedPath == nil || isSelectedProtected)
                .opacity(selectedPath == nil || isSelectedProtected ? 0.5 : 1)
            }
        }
        .padding(16)
    }

    private var isSelectedProtected: Bool {
        candidates.first { $0.path == selectedPath }?.isProtected ?? false
    }

    private func shortName(_ path: String?) -> String {
        guard let path else { return "" }
        return (path as NSString).lastPathComponent
    }
}

struct RestoreRow: View {
    let item: ConcealedItem
    let selected: Bool
    let onTap: () -> Void

    var body: some View {
        Button(action: onTap) {
            HStack(spacing: 11) {
                Image(systemName: "folder.fill")
                    .foregroundStyle(item.isProtected ? Color.secondary : Brand.warn)
                    .font(.title3)
                VStack(alignment: .leading, spacing: 1) {
                    Text(item.name).fontWeight(.semibold)
                    Text(item.attrs.joined(separator: " · ")).font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                if item.isProtected {
                    Text("mục hệ thống").font(.caption2.weight(.medium))
                        .padding(.horizontal, 7).padding(.vertical, 3)
                        .background(.quaternary, in: Capsule())
                } else if selected {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(Brand.primary).font(.title3)
                }
            }
            .padding(11)
            .background {
                RoundedRectangle(cornerRadius: 11)
                    .fill(selected ? Brand.primary.opacity(0.12) : Color(nsColor: .controlBackgroundColor))
            }
            .overlay(RoundedRectangle(cornerRadius: 11)
                .stroke(selected ? Brand.primary.opacity(0.5) : .primary.opacity(0.06)))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(item.isProtected)
        .opacity(item.isProtected ? 0.6 : 1)
    }
}
