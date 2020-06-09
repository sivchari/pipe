import { makeStyles } from "@material-ui/core";
import { Cached, CheckCircle, Error, Info } from "@material-ui/icons";
import { ApplicationSyncStatus } from "pipe/pkg/app/web/model/application_pb";
import React, { FC } from "react";

const useStyles = makeStyles((theme) => ({
  [ApplicationSyncStatus.UNKNOWN]: {
    color: theme.palette.grey[500],
  },
  [ApplicationSyncStatus.SYNCED]: {
    color: theme.palette.success.main,
  },
  [ApplicationSyncStatus.DEPLOYING]: {
    color: theme.palette.info.main,
  },
  [ApplicationSyncStatus.OUT_OF_SYNC]: {
    color: theme.palette.error.main,
  },
}));

interface Props {
  status: ApplicationSyncStatus;
}

export const SyncStatusIcon: FC<Props> = ({ status }) => {
  const classes = useStyles();

  switch (status) {
    case ApplicationSyncStatus.UNKNOWN:
      return <Info className={classes[status]} />;
    case ApplicationSyncStatus.SYNCED:
      return <CheckCircle className={classes[status]} />;
    case ApplicationSyncStatus.DEPLOYING:
      return <Cached className={classes[status]} />;
    case ApplicationSyncStatus.OUT_OF_SYNC:
      return <Error className={classes[status]} />;
  }
};
