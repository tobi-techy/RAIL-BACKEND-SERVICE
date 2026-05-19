import React from 'react';
import {
  AbsoluteFill,
  Audio,
  Easing,
  interpolate,
  Sequence,
  spring,
  staticFile,
  useCurrentFrame,
  useVideoConfig,
} from 'remotion';

const C = {
  warmCanvas: '#fbfaf9',
  stoneSurface: '#f2f0ed',
  parchmentCard: '#f8f7f4',
  graphite: '#474645',
  charcoal: '#343433',
  midnight: '#121212',
  ash: '#848281',
  fog: '#c6c6c6',
  ember: '#ff3e00',
  green: '#00ca48',
  blue: '#0090ff',
};

const clamp = {
  extrapolateLeft: 'clamp' as const,
  extrapolateRight: 'clamp' as const,
};

const fontCss = `
@font-face{font-family:SFProDisplay;src:url(${staticFile('fonts/SF-Pro-Display-Regular.otf')}) format('opentype');font-weight:400}
@font-face{font-family:SFProDisplay;src:url(${staticFile('fonts/SF-Pro-Display-Medium.otf')}) format('opentype');font-weight:500}
@font-face{font-family:SFProDisplay;src:url(${staticFile('fonts/SF-Pro-Display-Semibold.otf')}) format('opentype');font-weight:600}
@font-face{font-family:SFProDisplay;src:url(${staticFile('fonts/SF-Pro-Display-Bold.otf')}) format('opentype');font-weight:700}
@font-face{font-family:SFMono;src:url(${staticFile('fonts/SF-Mono-Regular.otf')}) format('opentype');font-weight:400}
@font-face{font-family:SFMono;src:url(${staticFile('fonts/SF-Mono-Medium.otf')}) format('opentype');font-weight:500}
@font-face{font-family:SFMono;src:url(${staticFile('fonts/SF-Mono-Semibold.otf')}) format('opentype');font-weight:600}
`;

const type = {
  fontFamily: 'SFProDisplay, -apple-system, BlinkMacSystemFont, sans-serif',
  letterSpacing: 0,
};

const mono = {
  fontFamily: 'SFMono, ui-monospace, Menlo, monospace',
  fontVariantNumeric: 'tabular-nums' as const,
  letterSpacing: 0,
};

const ease = Easing.bezier(0.22, 1, 0.36, 1);

const rise = (frame: number, delay = 0) =>
  spring({
    frame: frame - delay,
    fps: 30,
    config: {damping: 200, stiffness: 120, mass: 0.9},
  });

const localFade = (frame: number, duration: number) =>
  Math.min(
    interpolate(frame, [0, 18], [0, 1], clamp),
    interpolate(frame, [duration - 20, duration], [1, 0], clamp),
  );

const Shell: React.FC<{duration: number; dark?: boolean; children: React.ReactNode}> = ({
  duration,
  dark = false,
  children,
}) => {
  const frame = useCurrentFrame();
  const opacity = localFade(frame, duration);

  return (
    <AbsoluteFill
      style={{
        opacity,
        background: dark
          ? `linear-gradient(145deg, ${C.midnight}, #24211e)`
          : `linear-gradient(145deg, ${C.warmCanvas}, ${C.parchmentCard})`,
        color: dark ? C.warmCanvas : C.charcoal,
        ...type,
      }}
    >
      <Grid dark={dark} />
      {children}
    </AbsoluteFill>
  );
};

const Grid: React.FC<{dark?: boolean}> = ({dark = false}) => (
  <div
    style={{
      position: 'absolute',
      inset: 0,
      opacity: dark ? 0.11 : 0.38,
      backgroundImage:
        `linear-gradient(${dark ? 'rgba(251,250,249,0.11)' : 'rgba(52,52,51,0.07)'} 1px, transparent 1px), ` +
        `linear-gradient(90deg, ${dark ? 'rgba(251,250,249,0.11)' : 'rgba(52,52,51,0.07)'} 1px, transparent 1px)`,
      backgroundSize: '56px 56px',
    }}
  />
);

const RailGlyph: React.FC<{size: number}> = ({size}) => (
  <div style={{width: size, height: size, borderRadius: size * 0.28, background: C.ember, position: 'relative'}}>
    {[0.25, 0.45, 0.62].map((top, i) => (
      <div
        key={top}
        style={{
          position: 'absolute',
          left: size * 0.2,
          top: size * top,
          width: size * (i === 0 ? 0.54 : i === 1 ? 0.44 : 0.34),
          height: size * 0.1,
          borderRadius: 999,
          background: '#fff',
          transform: 'rotate(-28deg)',
        }}
      />
    ))}
    <div
      style={{
        position: 'absolute',
        right: size * 0.2,
        bottom: size * 0.22,
        width: size * 0.16,
        height: size * 0.16,
        borderRadius: 999,
        background: '#fff',
      }}
    />
  </div>
);

const Logo: React.FC<{dark?: boolean}> = ({dark = false}) => (
  <div style={{display: 'flex', alignItems: 'center', gap: 18}}>
    <RailGlyph size={62} />
    <div style={{...type, color: dark ? C.warmCanvas : C.charcoal, fontSize: 34, fontWeight: 700}}>
      Rail Money
    </div>
  </div>
);

const Kicker: React.FC<{children: React.ReactNode; dark?: boolean}> = ({children, dark = false}) => (
  <div style={{display: 'flex', alignItems: 'center', gap: 14, color: dark ? C.warmCanvas : C.ember, fontSize: 24, fontWeight: 600}}>
    <span style={{width: 34, height: 2, background: C.ember}} />
    {children}
  </div>
);

const Headline: React.FC<{children: React.ReactNode; size?: number}> = ({children, size = 104}) => {
  const frame = useCurrentFrame();
  const p = rise(frame, 4);
  return (
    <h1
      style={{
        margin: '30px 0 0',
        maxWidth: 1030,
        fontSize: size,
        lineHeight: 1.02,
        fontWeight: 700,
        letterSpacing: -1.8,
        opacity: p,
        transform: `translateY(${interpolate(p, [0, 1], [38, 0], clamp)}px)`,
      }}
    >
      {children}
    </h1>
  );
};

const MonoLine: React.FC<{children: React.ReactNode; delay?: number; color?: string}> = ({
  children,
  delay = 0,
  color = C.charcoal,
}) => {
  const frame = useCurrentFrame();
  const p = rise(frame, delay);
  return (
    <div
      style={{
        ...mono,
        opacity: p,
        transform: `translateY(${interpolate(p, [0, 1], [18, 0], clamp)}px)`,
        color,
        fontSize: 34,
        lineHeight: 1.55,
        fontWeight: 500,
      }}
    >
      {children}
    </div>
  );
};

const TitleScene: React.FC = () => (
  <Shell duration={165}>
    <div style={{position: 'relative', zIndex: 1, padding: '84px 104px', height: '100%', display: 'flex', flexDirection: 'column'}}>
      <Logo />
      <div style={{flex: 1, display: 'flex', alignItems: 'center'}}>
        <div>
          <Kicker>The real story</Kicker>
          <Headline>Money should move.</Headline>
          <div style={{color: C.ash, fontSize: 46, fontWeight: 500, marginTop: 34}}>The moment it arrives.</div>
        </div>
      </div>
    </div>
  </Shell>
);

const ProblemScene: React.FC = () => (
  <Shell duration={165}>
    <div style={{position: 'relative', zIndex: 1, padding: '112px 124px', height: '100%', display: 'grid', gridTemplateColumns: '1fr 0.74fr', alignItems: 'center', gap: 72}}>
      <div>
        <Kicker>The old default</Kicker>
        <Headline size={92}>Most apps ask you to manage.</Headline>
      </div>
      <div style={{display: 'grid', gap: 22}}>
        <MonoLine delay={18}>choose assets</MonoLine>
        <MonoLine delay={32}>time markets</MonoLine>
        <MonoLine delay={46}>switch apps</MonoLine>
        <MonoLine delay={60} color={C.ember}>leave cash idle</MonoLine>
      </div>
    </div>
  </Shell>
);

const RuleScene: React.FC = () => {
  const frame = useCurrentFrame();
  const seventy = rise(frame, 22);
  const thirty = rise(frame, 38);

  return (
    <Shell duration={240}>
      <div style={{position: 'relative', zIndex: 1, padding: '104px 118px', height: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'center'}}>
        <Kicker>One rule</Kicker>
        <Headline size={84}>Every deposit splits itself.</Headline>
        <div style={{display: 'flex', gap: 24, marginTop: 56}}>
          {[
            ['70%', 'Base Rail', C.ember, seventy],
            ['30%', 'Active Rail', C.green, thirty],
          ].map(([value, label, color, p]) => (
            <div
              key={label as string}
              style={{
                width: 390,
                borderRadius: 17,
                background: '#fff',
                boxShadow: `inset 0 0 0 1px ${C.stoneSurface}`,
                padding: '34px 38px',
                opacity: p as number,
                transform: `translateY(${interpolate(p as number, [0, 1], [26, 0], clamp)}px)`,
              }}
            >
              <div style={{...mono, color: color as string, fontSize: 92, fontWeight: 600, lineHeight: 1}}>{value}</div>
              <div style={{color: C.ash, fontSize: 28, fontWeight: 600, marginTop: 16}}>{label}</div>
            </div>
          ))}
        </div>
      </div>
    </Shell>
  );
};

const PositionScene: React.FC = () => (
  <Shell duration={180} dark>
    <div style={{position: 'relative', zIndex: 1, padding: '112px 124px', height: '100%', display: 'grid', gridTemplateColumns: '1fr 0.8fr', gap: 80, alignItems: 'center'}}>
      <div>
        <Kicker dark>Position</Kicker>
        <Headline size={94}>Delegated momentum.</Headline>
      </div>
      <div style={{display: 'grid', gap: 26}}>
        <MonoLine delay={18} color={C.fog}>not a trading app</MonoLine>
        <MonoLine delay={34} color={C.fog}>not a crypto exchange</MonoLine>
        <MonoLine delay={50} color={C.ember}>the system allocates</MonoLine>
      </div>
    </div>
  </Shell>
);

const CloseScene: React.FC = () => (
  <Shell duration={180}>
    <div style={{position: 'relative', zIndex: 1, padding: '84px 104px', height: '100%', display: 'flex', flexDirection: 'column', justifyContent: 'space-between'}}>
      <Logo />
      <div>
        <Kicker>Rail Money</Kicker>
        <Headline>Progress without managing money.</Headline>
      </div>
      <div style={{...mono, color: C.ash, fontSize: 24}}>rule.engine / 70:30 / always on</div>
    </div>
  </Shell>
);

export const RailMoneyStory: React.FC = () => {
  return (
    <AbsoluteFill style={{background: C.warmCanvas}}>
      <style>{fontCss}</style>
      <Audio src={staticFile('audio/rail-ambient-bed.wav')} volume={0.12} />
      <Audio src={staticFile('audio/rail-voiceover.wav')} volume={1} />
      <Sequence from={0} durationInFrames={165} premountFor={30}>
        <TitleScene />
      </Sequence>
      <Sequence from={165} durationInFrames={165} premountFor={30}>
        <ProblemScene />
      </Sequence>
      <Sequence from={330} durationInFrames={240} premountFor={30}>
        <RuleScene />
      </Sequence>
      <Sequence from={570} durationInFrames={180} premountFor={30}>
        <PositionScene />
      </Sequence>
      <Sequence from={750} durationInFrames={180}>
        <CloseScene />
      </Sequence>
    </AbsoluteFill>
  );
};
