import SwiftUI

struct ContentView: View {
    @State private var state = AppState()
    @State private var showQuickRestore = false
    @State private var deviceMonitor = DeviceMonitor()

    var body: some View {
        NavigationSplitView {
            DeviceListView(state: state)
                .navigationSplitViewColumnWidth(min: 240, ideal: 280)
        } detail: {
            if let part = state.selected {
                VolumeDetailView(state: state, partition: part, showQuickRestore: $showQuickRestore)
            } else {
                EmptyStateView(reachable: state.isDaemonReachable)
            }
        }
        .task {
            await state.refreshDevices()

            // Cơ chế chính: DiskArbitration báo ngay khi có ổ cắm vào/rút ra.
            deviceMonitor.start {
                Task { await state.refreshDevices() }
            }

            // Lưới an toàn: poll chậm phòng khi DiskArbitration bỏ sót sự kiện hiếm gặp.
            // Không phải cơ chế chính, chỉ dự phòng.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                await state.refreshDevices()
            }
        }
        .onDisappear {
            deviceMonitor.stop()
        }
        .sheet(isPresented: $showQuickRestore) {
            QuickRestoreView(state: state)
        }
        .alert("Có lỗi xảy ra", isPresented: .init(
            get: { state.errorMessage != nil },
            set: { if !$0 { state.errorMessage = nil } }
        )) {
            Button("Đóng", role: .cancel) { state.errorMessage = nil }
        } message: {
            Text(state.errorMessage ?? "")
        }
        .overlay {
            if state.isBusy {
                BusyOverlay(message: state.busyMessage)
            }
        }
    }
}

struct EmptyStateView: View {
    let reachable: Bool

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: reachable ? "externaldrive" : "exclamationmark.triangle")
                .font(.system(size: 48))
                .foregroundStyle(.secondary)
            Text(reachable ? "Chọn một ổ ở danh sách bên trái" : "Chưa kết nối được dịch vụ nền")
                .font(.title3)
            if !reachable {
                VStack(spacing: 4) {
                    Text("Cài dịch vụ nền một lần:")
                        .foregroundStyle(.secondary)
                    Text("make install-service")
                        .font(.callout.monospaced())
                        .textSelection(.enabled)
                    Text("hoặc chạy tạm: sudo doctorx-core serve")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct BusyOverlay: View {
    let message: String

    var body: some View {
        ZStack {
            Color.black.opacity(0.25).ignoresSafeArea()
            VStack(spacing: 12) {
                ProgressView()
                if !message.isEmpty {
                    Text(message).font(.callout)
                }
            }
            .padding(24)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        }
    }
}

struct DeviceListView: View {
    let state: AppState

    var body: some View {
        List(selection: .init(
            get: { state.selected?.bsd },
            set: { bsd in
                if let p = allPartitions.first(where: { $0.bsd == bsd }) { state.select(p) }
            }
        )) {
            ForEach(state.disks) { disk in
                Section {
                    ForEach(disk.partitions) { part in
                        PartitionRow(partition: part).tag(part.bsd)
                            .disabled(part.isSystem)
                    }
                } header: {
                    Text("\(disk.model) — \(formatBytes(disk.sizeBytes))")
                }
            }
        }
        .overlay {
            if state.disks.isEmpty {
                ContentUnavailableView(
                    "Chưa cắm ổ nào",
                    systemImage: "externaldrive.badge.questionmark",
                    description: Text("Cắm USB hoặc ổ cứng gắn ngoài để bắt đầu.")
                )
            }
        }
        .navigationTitle("DoctorX")
    }

    private var allPartitions: [Partition] {
        state.disks.flatMap(\.partitions)
    }
}

struct PartitionRow: View {
    let partition: Partition

    var body: some View {
        HStack {
            Image(systemName: partition.isSystem ? "lock.fill" : "internaldrive")
                .foregroundStyle(partition.isSystem ? .secondary : .primary)
            VStack(alignment: .leading, spacing: 2) {
                Text(partition.displayName)
                Text("\(partition.fsType.uppercased()) · \(formatBytes(partition.sizeBytes))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .help(partition.isSystem ? "Phân vùng hệ thống — DoctorX không thao tác lên đây." : "")
    }
}
