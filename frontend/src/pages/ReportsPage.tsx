import { useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import axios from 'axios'
import { DownloadIcon, ImageDownIcon } from 'lucide-react'
import { extractError } from '@/api/client'
import { getKpi } from '@/api/reports'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { IMAGE_FAILED, useImageExport } from '@/components/reports/exportElement'
import { ReportCard, ReportCardLoading } from '@/components/reports/ReportCard'
import { ReportsControls } from '@/components/reports/ReportsControls'
import { useSiteBranding } from '@/hooks/useSiteBranding'
import { saveDataUrl } from '@/lib/saveFile'
import { useFilterLookups } from '@/lib/useFilterLookups'
import { parseTicketSearch } from '@/lib/ticketSearch'
import {
  KPI_CARD_ORDER,
  filterLine,
  kpiCardModels,
  kpiCsv,
  kpiFileName,
  kpiSearchFromRecord,
  kpiSearchToRecord,
  localDay,
  periodLine,
  presetRange,
  toKpiParams,
  type KpiNames,
  type KpiSearch,
} from '@/lib/kpi'
import type { KpiResponse } from '@/api/types'

// The Reports page (docs/DESIGN.md, "KPI reports"): the controls, then the
// board. The board is the part that is exported; its header names the period
// and the filters so that an image of it says what it shows.

// The filter selects offer the ticket list's lists; nothing of a ticket search
// is in play here.
const NO_TICKET_SEARCH = parseTicketSearch({})

const browserTZ = () => Intl.DateTimeFormat().resolvedOptions().timeZone

const isBadRequest = (err: unknown) => axios.isAxiosError(err) && err.response?.status === 400

const csvUrl = (text: string) => `data:text/csv;charset=utf-8,${encodeURIComponent(text)}`

export function ReportsPage() {
  const navigate = useNavigate()
  const search = kpiSearchFromRecord(useSearch({ from: '/reports' }) as Record<string, unknown>)
  const { name: siteName } = useSiteBranding()
  const board = useRef<HTMLDivElement>(null)
  const boardImage = useImageExport()

  // The address is the state: a change writes it, and the request follows it.
  function change(s: KpiSearch) {
    void navigate({ to: '/reports', search: kpiSearchToRecord(s) as never })
  }

  const tz = browserTZ()
  const today = localDay(new Date())
  const params = toKpiParams(search, tz, today)
  const kpi = useQuery({
    queryKey: ['reports', 'kpi', params],
    queryFn: () => getKpi(params),
    staleTime: 60_000,
    // A request the server refused would be refused again.
    retry: (count, err) => count < 1 && !isBadRequest(err),
    retryDelay: 300,
  })
  const resp: KpiResponse | undefined = kpi.data
  const refused = kpi.error && isBadRequest(kpi.error) ? extractError(kpi.error) : null

  const lookups = useFilterLookups(NO_TICKET_SEARCH, { staff: true, wantPeople: true })
  const names: KpiNames = {
    category: lookups.categories.find((c) => c.id === resp?.filters.category_id)?.name,
    group: lookups.groups.find((g) => g.id === resp?.filters.assignee_group_id)?.name,
  }

  const shown =
    search.range.kind === 'preset'
      ? presetRange(search.range.days, today)
      : { from: search.range.from ?? today, to: search.range.to ?? today }

  // What the strip under every image says.
  const contextLines = () =>
    resp ? [periodLine(resp), filterLine(resp.filters, names), `Created ${new Date().toLocaleString()} · ${siteName}`] : []

  return (
    <Layout>
      <div className="space-y-6">
        <ReportsControls
          search={search}
          onChange={change}
          shown={shown}
          categories={lookups.categories}
          groups={lookups.groups}
          message={refused}
        />

        <div id="reports-board" ref={board} className="space-y-4">
          <header className="space-y-1">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0 space-y-1">
                <h1 className="text-2xl font-bold text-gray-900">Reports</h1>
                {resp && (
                  <>
                    <p className="text-sm text-gray-700 wrap-anywhere">{periodLine(resp)}</p>
                    <p className="text-sm text-gray-500 wrap-anywhere">{filterLine(resp.filters, names)}</p>
                  </>
                )}
              </div>
              <div data-export="skip" className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  variant="outline"
                  className="pointer-coarse:min-h-11"
                  disabled={!resp}
                  onClick={() => resp && saveDataUrl(csvUrl(kpiCsv(resp, names)), kpiFileName(resp, 'csv'))}
                >
                  <DownloadIcon className="mr-2 h-4 w-4" aria-hidden="true" />
                  Download CSV
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  className="pointer-coarse:min-h-11"
                  disabled={!resp || boardImage.busy}
                  aria-busy={boardImage.busy ? 'true' : undefined}
                  onClick={() => resp && void boardImage.run(board.current, contextLines(), kpiFileName(resp, 'png'))}
                >
                  <ImageDownIcon className="mr-2 h-4 w-4" aria-hidden="true" />
                  Download image
                </Button>
              </div>
            </div>
            {boardImage.failed && (
              <p data-export="skip" className="text-sm text-red-700">
                {IMAGE_FAILED}
              </p>
            )}
          </header>

          {resp ? (
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {kpiCardModels(resp).map((m) => (
                <ReportCard key={m.key} model={m} resp={resp} contextLines={contextLines} />
              ))}
            </div>
          ) : kpi.isError ? (
            refused ? null : (
              <div
                data-export="skip"
                role="alert"
                className="flex flex-wrap items-center gap-3 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800"
              >
                <span>The reports could not be loaded.</span>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="pointer-coarse:min-h-11"
                  onClick={() => void kpi.refetch()}
                  disabled={kpi.isFetching}
                >
                  Retry
                </Button>
              </div>
            )
          ) : (
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {KPI_CARD_ORDER.map((k) => (
                <ReportCardLoading key={k} cardKey={k} />
              ))}
            </div>
          )}
        </div>
      </div>
    </Layout>
  )
}
