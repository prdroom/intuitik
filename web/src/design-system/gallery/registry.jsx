// Renders each gallery item with its component. Interactive items write the events their
// component emits into a visible event log, so that a browser check can drive the real
// element and read what it emitted (packet DS-1 B-1).
import { useState } from 'react';
import {
  AnswerBlock,
  AttentionPanel,
  Banner,
  Breadcrumb,
  Button,
  Chip,
  ClusterBandItem,
  CodeBlock,
  ConnectionStateCard,
  ConsentCard,
  DefinitionList,
  EmptyState,
  ErrorCard,
  GhostCard,
  Icon,
  InspectorShell,
  Link,
  LinkEnds,
  LoadingProgress,
  NamespaceTile,
  NodeCard,
  NotSetUpCard,
  Port,
  RefRow,
  ReviewPanel,
  SendSummary,
  StatePill,
  StatusTab,
  TopBar,
  ZoomControl,
} from '../index.js';
import { GALLERY_TEXT, TILE_LABELS } from './fixtures.js';

function CardDemo({ item, log }) {
  const { props } = item;
  const [mode, setMode] = useState(props.mode ?? 'compact');
  const [centres, setCentres] = useState(null);
  const togglable = Boolean(props.owned) && item.id === 'C-14.compact.folded';
  return (
    <div className="gallery-card-demo">
      {togglable && (
        <div className="gallery-card-demo__controls">
          <Button
            variant="secondary"
            onClick={() => {
              const next = mode === 'compact' ? 'expanded' : 'compact';
              setMode(next);
              log(next === 'expanded' ? 'expand' : 'collapse');
            }}
          >
            {mode === 'compact' ? GALLERY_TEXT.expand : GALLERY_TEXT.collapse}
          </Button>
        </div>
      )}
      <div className="gallery-card-host">
        <NodeCard {...props} mode={mode} onPortCentres={setCentres} />
      </div>
      <div className="gallery-centres">
        <span>{GALLERY_TEXT.portCentres}</span>
        <output data-port-centres={item.id}>{centres ? JSON.stringify(centres) : ''}</output>
      </div>
    </div>
  );
}

function TopBarDemo({ item, log }) {
  const [context, setContext] = useState(item.props.context);
  return (
    <TopBar
      {...item.props}
      context={context}
      contextLabel="Context"
      onContextChange={(c) => {
        setContext(c);
        log(`context ${c}`);
      }}
      onFind={() => log('find')}
      onSettings={() => log('settings')}
      onAsk={() => log('ask')}
    />
  );
}

function StageDemo({ item, log }) {
  const [stage, setStage] = useState(item.props.stage);
  return (
    <Breadcrumb
      path={item.props.path}
      stage={stage}
      onNavigate={(s) => {
        setStage(s);
        log(`navigate ${s}`);
      }}
    />
  );
}

function AttentionDemo({ item, log }) {
  const [selectedId, setSelectedId] = useState(item.props.selectedId ?? null);
  return (
    <AttentionPanel
      {...item.props}
      selectedId={selectedId}
      onSelect={(id) => {
        setSelectedId(id);
        log(`select ${id}`);
      }}
      onToggle={(group, open) => log(`toggle ${group} ${open ? 'open' : 'closed'}`)}
      onAsk={(id) => log(`ask ${id}`)}
    />
  );
}

const RENDER = {
  'C-01': (p) => <Icon status={p.status} kind={p.kind} />,
  'C-02': (p) => (
    <Chip tone={p.tone} font={p.font}>
      {p.text}
    </Chip>
  ),
  'C-03': (p) => <StatePill kind={p.kind} state={p.state} text={p.text} />,
  'C-04': (p, log) => (
    <Button variant={p.variant} disabled={p.disabled} onClick={() => log('click')}>
      {p.text}
    </Button>
  ),
  'C-05': (p, log) => <CodeBlock text={p.text} copiedText={p.copiedText} failedText={p.failedText} onCopy={(r) => log(`copy ${r}`)} />,
  'C-10': (p) => (
    <div className="gallery-tab-host">
      <StatusTab state={p.state} text={p.text} brokenLinks={p.brokenLinks} />
    </div>
  ),
  'C-11': (p) => (
    <div className="gallery-row-host">
      <RefRow {...p.row} />
    </div>
  ),
  'C-12': (p) => (
    <div className="gallery-port-host">
      <Port direction={p.direction} state={p.state} />
    </div>
  ),
  'C-13': (p) => <Link from={{ x: 8, y: 12 }} to={{ x: 232, y: 52 }} width={240} height={64} state={p.state} kind={p.kind} fromLabel={p.fromLabel} toLabel={p.toLabel} text={p.text} />,
  'C-14': (p, log, item) => <CardDemo item={item} log={log} />,
  'C-15': (p) => <GhostCard {...p} />,
  'C-16': (p) => <NamespaceTile {...p} labels={TILE_LABELS} />,
  'C-17': (p) => <ClusterBandItem {...p} />,
  'C-20': (p, log, item) => <TopBarDemo item={item} log={log} />,
  'C-21': (p, log, item) => <StageDemo item={item} log={log} />,
  'C-22': (p, log, item) => <AttentionDemo item={item} log={log} />,
  'C-23': (p, log) => (
    <InspectorShell kind={p.kind} name={p.name} state={p.state} reason={p.reason} initialTab={p.initialTab} onTabChange={(t) => log(`tab ${t}`)} onSubTabChange={(t) => log(`subtab ${t}`)}>
      <DefinitionList
        items={[
          { key: 'Kind', value: p.kind },
          { key: 'Name', value: p.name },
        ]}
      />
    </InspectorShell>
  ),
  'C-24': (p) => <LinkEnds {...p} />,
  'C-25': (p) => <DefinitionList items={p.items} />,
  'C-26': (p, log) => <ZoomControl {...p} onZoomIn={() => log('zoom in')} onZoomOut={() => log('zoom out')} />,
  'C-30': (p) => <SendSummary {...p} />,
  'C-31': (p, log) => <ReviewPanel {...p} onChange={(s) => log(`selection ${JSON.stringify(s)}`)} />,
  'C-32': (p, log) => <AnswerBlock {...p} onEvidence={(e) => log(`evidence ${e}`)} onAttach={() => log('attach')} onCopy={(r) => log(`copy ${r}`)} />,
  'C-33': (p, log) => (
    <ConsentCard {...p} onLogDefaultChange={(v) => log(`logs ${v}`)} onAgree={() => log('agree')} onDecline={() => log('decline')} />
  ),
  'C-34': (p, log, item) =>
    item.id === 'C-34.error' ? <ErrorCard text={p.text} onRetry={() => log('retry')} /> : <NotSetUpCard text={p.text} onOpenSettings={() => log('open settings')} />,
  'C-40': (p, log) => <ConnectionStateCard {...p} onRetry={() => log('retry')} onChooseContext={() => log('choose context')} />,
  'C-41': (p) => <Banner variant={p.variant} text={p.text} />,
  'C-42': (p) => <LoadingProgress received={p.received} total={p.total} />,
  'C-43': (p) => <EmptyState text={p.text} />,
};

export const RENDERED_COMPONENTS = Object.keys(RENDER);

// One gallery item: its id and title, the rendered component and its event log.
export function GalleryItem({ item }) {
  const [events, setEvents] = useState([]);
  const log = (entry) => setEvents((prev) => [...prev, entry]);
  const render = RENDER[item.component];
  return (
    <section className="gallery-item" id={item.id} data-gallery-item={item.id}>
      <h3 className="gallery-item__title">
        <code>{item.id}</code> {item.title}
      </h3>
      <div className="gallery-item__body">{render(item.props, log, item)}</div>
      <div className="gallery-item__events">
        <span>{GALLERY_TEXT.events}</span>
        <ol className="gallery-log" data-event-log={item.id}>
          {events.map((e, i) => (
            <li key={i}>{e}</li>
          ))}
        </ol>
      </div>
    </section>
  );
}
