import { useEffect, useMemo, useRef, useState } from "react";
import { geoAlbersUsa, geoPath } from "d3-geo";
import { feature, mesh } from "topojson-client";
import type { Topology, GeometryCollection } from "topojson-specification";
import type { FeatureCollection, Geometry, MultiLineString } from "geojson";
import { MapPin, Pencil, Search, X } from "lucide-react";
import { getNodes } from "./api";
import { formatAddress, formatDailyHours, hoursKey, isLocationCI, localClock, locationCoordinates, locationOpenNow, weekdays } from "./locations";
import type { GraphNode } from "./types";

// Albers USA places Alaska and Hawaii as insets below the lower 48, so every
// state fits one SVG without panning. The state outlines are vendored from
// us-atlas as /us-states-10m.json.
const statesURL = "/us-states-10m.json";
const mapWidth = 975;
const mapHeight = 610;

type OpenState = "open" | "closed" | null;

interface Site {
  node: GraphNode;
  name: string;
  state: OpenState;
  point: { latitude: number; longitude: number; precision: string } | null;
  xy: [number, number] | null;
}

interface StateShapes {
  states: FeatureCollection<Geometry>;
  borders: MultiLineString;
}

function stateLabel(state: OpenState) {
  return state === "open" ? "Open now" : state === "closed" ? "Closed now" : "Hours unknown";
}

function todayHours(site: Site) {
  const clock = localClock(site.node.properties ?? {});
  if (!clock) return "";
  const hours = formatDailyHours(site.node.properties?.[hoursKey(clock.weekday)], "");
  return `${clock.weekday}: ${hours || "hours not set"} · local time ${clock.time}`;
}

function SitePanel({ site, onClose, onEdit }: { site: Site; onClose: () => void; onEdit: (record: GraphNode) => void }) {
  const properties = site.node.properties ?? {};
  return <aside className="map-inspector" aria-label="Selected site">
    <header className="map-inspector-header">
      <div><p className="eyebrow">SELECTED SITE</p><h2>{site.name}</h2></div>
      <button className="close-button" type="button" onClick={onClose} aria-label="Close site details"><X size={16} /></button>
    </header>
    <div className="map-inspector-meta">
      <span>{properties.siteId ? `Site ${String(properties.siteId)}` : "No site ID"}</span>
      <span className={`status-pill ${site.state === "open" ? "status-active" : "status-closed"}`}>{stateLabel(site.state)}</span>
    </div>
    <code className="map-inspector-id">{site.node.id}</code>
    <section className="trace-section">
      <div className="trace-section-heading"><h3>Address</h3></div>
      <p className="site-detail">{formatAddress(properties) || "No address recorded."}</p>
      <p className="site-detail muted">{site.point
        ? `${site.point.latitude.toFixed(4)}, ${site.point.longitude.toFixed(4)}${site.point.precision === "state" ? " · approximate (state center)" : site.point.precision === "manual" ? " · entered manually" : ""}`
        : "Not plotted: no coordinates could be derived from the address."}</p>
    </section>
    <section className="trace-section">
      <div className="trace-section-heading"><h3>Hours of operation</h3><span>{String(properties.timezone ?? "local")}</span></div>
      <p className="site-detail">{todayHours(site)}</p>
      <div className="site-hours">{weekdays.map((day) => <div className="site-hours-row" key={day}><span>{day}</span><strong>{formatDailyHours(properties[hoursKey(day)])}</strong></div>)}</div>
    </section>
    <button className="button button-quiet icon-button site-edit" type="button" onClick={() => onEdit(site.node)}><Pencil size={14} />Edit location</button>
  </aside>;
}

export default function LocationMap({ reload, onEdit }: { reload: number; onEdit: (record: GraphNode) => void }) {
  const [shapes, setShapes] = useState<StateShapes | null>(null);
  const [locations, setLocations] = useState<GraphNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [now, setNow] = useState(() => new Date());
  const hoveredRef = useRef<string | null>(null);
  const [hoveredID, setHoveredID] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    fetch(statesURL).then((response) => {
      if (!response.ok) throw new Error(`Could not load state outlines (${response.status}).`);
      return response.json() as Promise<Topology<{ states: GeometryCollection }>>;
    }).then((topology) => {
      if (!active) return;
      setShapes({
        states: feature(topology, topology.objects.states) as FeatureCollection<Geometry>,
        borders: mesh(topology, topology.objects.states, (a, b) => a !== b),
      });
    }).catch((loadError: unknown) => {
      if (active) setError(loadError instanceof Error ? loadError.message : "Could not load the map.");
    });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    getNodes("ci", false).then((nodes) => {
      if (!active) return;
      setLocations(nodes.filter(isLocationCI));
      setError("");
    }).catch((loadError: unknown) => {
      if (!active) return;
      setError(loadError instanceof Error ? loadError.message : "Could not load locations.");
      setLocations([]);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [reload]);

  // Re-evaluate open/closed each minute so markers change colour on their own.
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const projection = useMemo(() => geoAlbersUsa().scale(1300).translate([mapWidth / 2, mapHeight / 2]), []);
  const path = useMemo(() => geoPath(projection), [projection]);

  const sites: Site[] = useMemo(() => locations.map((node): Site => {
    const properties = node.properties ?? {};
    const point = locationCoordinates(properties);
    const projected = point ? projection([point.longitude, point.latitude]) : null;
    return {
      node,
      name: typeof properties.name === "string" && properties.name ? properties.name : node.id,
      state: locationOpenNow(properties, now),
      point,
      xy: projected ? [projected[0], projected[1]] as [number, number] : null,
    };
  }).sort((a, b) => a.name.localeCompare(b.name)), [locations, projection, now]);

  const query = search.trim().toLowerCase();
  const visibleSites = sites.filter((site) => !query || JSON.stringify(site.node).toLowerCase().includes(query));
  const plotted = visibleSites.filter((site) => site.xy);
  const unplotted = visibleSites.filter((site) => !site.xy);
  const openCount = plotted.filter((site) => site.state === "open").length;
  const selected = sites.find((site) => site.node.id === selectedID) ?? null;
  const hovered = plotted.find((site) => site.node.id === hoveredID) ?? null;

  function markerClass(site: Site) {
    return `site-marker ${site.state === "open" ? "is-open" : site.state === "closed" ? "is-closed" : "is-unknown"} ${site.node.id === selectedID ? "is-selected" : ""} ${site.point?.precision === "state" ? "is-approximate" : ""}`;
  }

  return <section className="map-section" aria-label="Location map">
    <div className="map-toolbar">
      <label className="search-box map-search"><Search aria-hidden="true" size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} type="search" placeholder="Find a site, city, or state" /></label>
      <span className="map-totals">{loading ? "Loading sites…" : `${plotted.length} plotted · ${openCount} open · ${plotted.length - openCount} closed or unknown`}</span>
    </div>
    <div className={`map-workspace ${selected ? "has-inspector" : ""}`}>
      <div className="map-canvas site-map-canvas" onClick={() => setSelectedID(null)}>
        {!shapes && !error && <div className="map-overlay">Loading map…</div>}
        {error && <div className="map-overlay map-error">{error}</div>}
        {shapes && <svg className="site-map" viewBox={`0 0 ${mapWidth} ${mapHeight}`} role="img" aria-label="United States with site locations">
          <g className="site-map-states">
            {shapes.states.features.map((state, index) => <path key={state.id ?? index} d={path(state) ?? undefined} />)}
          </g>
          <path className="site-map-borders" d={path(shapes.borders) ?? undefined} />
          <g className="site-markers">
            {plotted.map((site) => <g
              key={site.node.id}
              className={markerClass(site)}
              transform={`translate(${site.xy![0]}, ${site.xy![1]})`}
              onClick={(event) => { event.stopPropagation(); setSelectedID(site.node.id); }}
              onMouseEnter={() => { hoveredRef.current = site.node.id; setHoveredID(site.node.id); }}
              onMouseLeave={() => { if (hoveredRef.current === site.node.id) { hoveredRef.current = null; setHoveredID(null); } }}
              tabIndex={0}
              role="button"
              aria-label={`${site.name}, ${stateLabel(site.state)}`}
              onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); setSelectedID(site.node.id); } }}
            >
              <circle className="site-marker-halo" r={13} />
              <circle className="site-marker-dot" r={6.5} />
            </g>)}
          </g>
          {hovered && hovered.xy && <g className="site-tooltip" transform={`translate(${Math.min(hovered.xy[0] + 14, mapWidth - 230)}, ${Math.max(hovered.xy[1] - 42, 8)})`} pointerEvents="none">
            <rect width={220} height={50} rx={3} />
            <text x={10} y={20} className="site-tooltip-title">{hovered.name.length > 28 ? `${hovered.name.slice(0, 27)}…` : hovered.name}</text>
            <text x={10} y={38} className="site-tooltip-detail">{stateLabel(hovered.state)}{hovered.node.properties?.city ? ` · ${String(hovered.node.properties.city)}, ${String(hovered.node.properties.state ?? "")}` : ""}</text>
          </g>}
        </svg>}
        {!loading && shapes && sites.length === 0 && <div className="map-overlay site-map-empty"><div><MapPin size={22} /><p>No location CIs yet. Add one with the button above and its address will be plotted here.</p></div></div>}
      </div>
      {selected && <SitePanel site={selected} onClose={() => setSelectedID(null)} onEdit={onEdit} />}
    </div>
    <div className="map-legend" aria-label="Map legend">
      <span><i className="legend-dot open" />Open now</span>
      <span><i className="legend-dot closed" />Closed now</span>
      <span><i className="legend-dot unknown" />Hours unknown</span>
      <span><i className="legend-dot approximate" />Approximate (state center)</span>
      {unplotted.length > 0 && <span className="map-unplotted">Not plotted: {unplotted.map((site) => site.name).join(", ")}</span>}
      <span className="map-totals">{visibleSites.length} site{visibleSites.length === 1 ? "" : "s"}</span>
    </div>
  </section>;
}
