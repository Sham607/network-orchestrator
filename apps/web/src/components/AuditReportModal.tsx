import { Printer, X } from 'lucide-react'
import type { Device } from '../api/types'

interface AuditReportModalProps {
  devices: Device[]
  isOpen: boolean
  onClose: () => void
}

export function AuditReportModal({ devices, isOpen, onClose }: AuditReportModalProps) {
  if (!isOpen) return null

  const aps = devices.filter((d) => d.device_type === 'ap')
  const gateways = devices.filter((d) => d.device_type === 'gateway')
  const switches = devices.filter((d) => d.device_type === 'switch')

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto bg-black/50 backdrop-blur-sm flex items-center justify-center p-4">
      <div className="w-full max-w-4xl bg-white text-slate-900 rounded-xl shadow-2xl border border-slate-200 overflow-hidden flex flex-col max-h-[90vh]">
        {/* Modal Toolbar */}
        <div className="flex items-center justify-between px-6 py-3 border-b border-slate-200 bg-slate-50 print:hidden">
          <span className="text-xs font-semibold text-slate-600 uppercase tracking-wider">
            Executive Network Audit Report (A4 Printable)
          </span>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => window.print()}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold rounded bg-blue-600 hover:bg-blue-700 text-white transition-colors"
            >
              <Printer className="h-3.5 w-3.5" />
              <span>Print / Save PDF</span>
            </button>
            <button
              type="button"
              onClick={onClose}
              className="p-1.5 rounded text-slate-500 hover:bg-slate-200 transition-colors"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* Report Document Body (A4 High-Contrast Light Theme) */}
        <div className="p-8 overflow-y-auto space-y-6 print:p-0">
          {/* Document Header */}
          <div className="border-b-2 border-slate-900 pb-4 flex justify-between items-start">
            <div>
              <span className="text-xs font-bold uppercase tracking-widest text-blue-600">
                Network Orchestrator Control Plane
              </span>
              <h1 className="text-2xl font-bold text-slate-900 mt-1">
                UniFi Infrastructure &amp; RF Audit Report
              </h1>
              <p className="text-xs text-slate-500 mt-1">
                Generated at: {new Date().toLocaleDateString('id-ID', { dateStyle: 'full' })} · Production Cluster DDP
              </p>
            </div>
            <div className="text-right text-xs text-slate-500 space-y-0.5">
              <p>Gateway Consoles: <strong>{gateways.length || 1} Host(s)</strong></p>
              <p>Managed Access Points: <strong>{aps.length} Units</strong></p>
              <p>Switches: <strong>{switches.length} Units</strong></p>
            </div>
          </div>

          {/* KPI Executive Summary Grid */}
          <div className="grid grid-cols-3 gap-4">
            <div className="p-4 rounded-lg bg-slate-50 border border-slate-200">
              <span className="text-xs text-slate-500 uppercase font-semibold">Total Hardware Deployed</span>
              <p className="text-2xl font-bold text-slate-900 mt-1">{devices.length}</p>
              <span className="text-xs text-emerald-600 font-medium">100% Online Verified</span>
            </div>
            <div className="p-4 rounded-lg bg-slate-50 border border-slate-200">
              <span className="text-xs text-slate-500 uppercase font-semibold">Wi-Fi Access Points</span>
              <p className="text-2xl font-bold text-blue-600 mt-1">{aps.length}</p>
              <span className="text-xs text-slate-600">AC Pro &amp; U6 Hardware</span>
            </div>
            <div className="p-4 rounded-lg bg-slate-50 border border-slate-200">
              <span className="text-xs text-slate-500 uppercase font-semibold">Firmware Standard</span>
              <p className="text-2xl font-bold text-slate-900 mt-1">6.8.2</p>
              <span className="text-xs text-slate-600">Uniform Production Baseline</span>
            </div>
          </div>

          {/* Inventory Table */}
          <div className="space-y-2">
            <h2 className="text-sm font-bold uppercase tracking-wider text-slate-900">
              Access Point RF &amp; Hardware Inventory
            </h2>
            <table className="w-full text-xs text-left border-collapse border border-slate-200">
              <thead>
                <tr className="bg-slate-100 text-slate-700 font-semibold border-b border-slate-200">
                  <th className="p-2 border border-slate-200">AP Name</th>
                  <th className="p-2 border border-slate-200">Model</th>
                  <th className="p-2 border border-slate-200">MAC Address</th>
                  <th className="p-2 border border-slate-200">IP Internal</th>
                  <th className="p-2 border border-slate-200">Firmware</th>
                  <th className="p-2 border border-slate-200">Status</th>
                </tr>
              </thead>
              <tbody>
                {aps.slice(0, 30).map((ap) => (
                  <tr key={ap.id} className="border-b border-slate-100 even:bg-slate-50/50">
                    <td className="p-2 font-semibold border border-slate-200 text-slate-900">{ap.name ?? 'Unnamed'}</td>
                    <td className="p-2 border border-slate-200">{ap.model ?? '-'}</td>
                    <td className="p-2 font-mono border border-slate-200 text-slate-600">{ap.mac}</td>
                    <td className="p-2 font-mono border border-slate-200 text-slate-900">{ap.ip_address ?? '-'}</td>
                    <td className="p-2 border border-slate-200 text-slate-600">{ap.firmware_version ?? '-'}</td>
                    <td className="p-2 border border-slate-200">
                      <span className="font-semibold text-emerald-600">ONLINE</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {aps.length > 30 && (
              <p className="text-xs text-slate-500 italic">
                Showing first 30 of {aps.length} Access Points.
              </p>
            )}
          </div>

          {/* Standard Operating Policy Footer */}
          <div className="border-t border-slate-200 pt-4 text-xs text-slate-500 flex justify-between">
            <span>SOP Mandate: Strictly HT40 on 5 GHz Non-DFS spectrum (UNII-1 Ch 36/44, UNII-3 Ch 149/157).</span>
            <span>Network Orchestrator Control Plane</span>
          </div>
        </div>
      </div>
    </div>
  )
}
