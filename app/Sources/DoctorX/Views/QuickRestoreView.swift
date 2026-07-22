import SwiftUI

/// Khôi phục thư mục bị giấu ở gốc ổ.
///
/// Đây là bản chuyển của `Khoi-phuc-thu-muc-an.bat`: liệt kê thư mục ẩn ở gốc,
/// cho chọn một, rồi gỡ cờ Hidden+System cho toàn cây con.
///
/// Khác script gốc ở chỗ thư mục hệ thống KHÔNG chọn được, thay vì hiện cảnh
/// báo rồi để người dùng tự cân nhắc.
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
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Khôi phục thư mục bị giấu").font(.title2.bold())
                Text("Chọn thư mục cần hiện lại. DoctorX sẽ gỡ cờ Hidden và System — "
                   + "cả hai đều phải gỡ thì Windows mới hiện thư mục ra.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            .padding()

            Divider()

            if candidates.isEmpty {
                ContentUnavailableView(
                    "Không có thư mục nào bị giấu ở gốc ổ",
                    systemImage: "checkmark.circle"
                )
                .frame(minHeight: 200)
            } else {
                List(candidates, selection: $selectedPath) { item in
                    HStack {
                        Image(systemName: "folder.badge.questionmark")
                        VStack(alignment: .leading, spacing: 2) {
                            Text(item.name)
                            Text(item.attrs.joined(separator: ", "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        if item.isProtected {
                            Text("mục hệ thống")
                                .font(.caption)
                                .padding(.horizontal, 6).padding(.vertical, 2)
                                .background(.quaternary, in: Capsule())
                        }
                    }
                    .tag(item.path)
                    .disabled(item.isProtected)
                }
                .frame(minHeight: 240)
            }

            Divider()

            VStack(alignment: .leading, spacing: 12) {
                Toggle("Áp dụng cho toàn bộ thư mục con bên trong", isOn: $recursive)

                HStack {
                    Text("Ổ sẽ được tháo tạm rồi gắn lại. Thao tác này hoàn tác được.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Đóng") { dismiss() }
                    Button("Khôi phục") { confirming = true }
                        .buttonStyle(.borderedProminent)
                        .disabled(selectedPath == nil || isSelectedProtected)
                }
            }
            .padding()
        }
        .frame(width: 560)
        .confirmationDialog(
            "Khôi phục \(selectedPath ?? "")?",
            isPresented: $confirming,
            titleVisibility: .visible
        ) {
            Button("Khôi phục") {
                if let path = selectedPath {
                    Task {
                        await state.unhide(path: path, recursive: recursive)
                        dismiss()
                    }
                }
            }
            Button("Huỷ", role: .cancel) {}
        } message: {
            Text(recursive
                 ? "Toàn bộ thư mục con bên trong cũng sẽ được hiện lại."
                 : "Chỉ thư mục này được hiện lại, các mục bên trong giữ nguyên.")
        }
    }

    private var isSelectedProtected: Bool {
        candidates.first { $0.path == selectedPath }?.isProtected ?? false
    }
}
