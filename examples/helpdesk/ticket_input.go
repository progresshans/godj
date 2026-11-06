package helpdesk

import (
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func (a *Application) prepareNewTicket(input orm.ModelInput[models.Ticket]) (orm.PreparedModel[models.Ticket], error) {
	scoped, err := models.TicketObjects.WithServerValues(input, map[string]query.Value{"category": query.Integer(a.categoryID)})
	if err != nil {
		return orm.PreparedModel[models.Ticket]{}, err
	}
	return models.TicketObjects.PrepareInput(scoped, nil)
}
