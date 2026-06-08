import React, { useState } from 'react';
import AutomationManager from './AutomationManager';

const EventManager: React.FC<{ projectId: string }> = ({ projectId }) => {
  return (
     <AutomationManager projectId={projectId} />
  );
};

export default EventManager;
