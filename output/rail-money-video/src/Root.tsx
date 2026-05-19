import React from 'react';
import {Composition} from 'remotion';
import {RailMoneyStory} from './RailMoneyStory';

export const Root: React.FC = () => (
  <Composition
    id="RailMoneyStory"
    component={RailMoneyStory}
    durationInFrames={930}
    fps={30}
    width={1920}
    height={1080}
  />
);
